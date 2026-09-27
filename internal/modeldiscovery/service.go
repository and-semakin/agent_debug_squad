package modeldiscovery

import (
	"context"
	"errors"
	"github.com/and-semakin/agent_debug_squad/internal/adapters"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/modelprobe"
	"math"
	"sync"
	"time"
)

type Factory func(context.Context, domain.AgentSpec, domain.MachineBackends, domain.ModelDiscoveryInput) (adapters.ModelLister, func(), error)

func DefaultTimeout(n int, b time.Duration) (time.Duration, error) {
	if n <= 0 || b <= 0 || b > math.MaxInt64-5*time.Second {
		return 0, errors.New("invalid_arguments")
	}
	waves := int64((n-1)/3 + 1)
	unit := b + 5*time.Second
	if waves > (math.MaxInt64-int64(5*time.Second))/int64(unit) {
		return 0, errors.New("invalid_arguments")
	}
	d := time.Duration(waves)*unit + 5*time.Second
	if d < 60*time.Second {
		d = 60 * time.Second
	}
	return d, nil
}
func Discover(ctx context.Context, targets []Target, machine domain.MachineBackends, overall, perTarget time.Duration, factory Factory) domain.ModelCatalog {
	if factory == nil {
		factory = adapters.NewModelLister
	}
	pass, cancel := context.WithTimeout(ctx, overall)
	defer cancel()
	report := domain.ModelCatalog{SchemaVersion: 1, GeneratedAt: time.Now().UTC(), Results: make([]domain.ModelDiscoveryResult, len(targets))}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				t := targets[i]
				func() {
					targetCtx, stop := context.WithTimeout(pass, perTarget)
					defer stop()
					r := modelprobe.New(t.Spec.Backend, "installation", "installation", "configured", t.Input)
					r.TargetID = t.ID
					r.Agents = t.Agents
					defer func() {
						if recover() != nil {
							modelprobe.Fail(&r, "error", "discovery_failed")
						}
						r.TargetID = t.ID
						r.Agents = t.Agents
						modelprobe.Finish(&r)
						report.Results[i] = r
					}()
					if targetCtx.Err() != nil {
						modelprobe.Fail(&r, modelprobe.ContextStatus(targetCtx, "error"), "context_ended")
						return
					}
					l, close, e := factory(targetCtx, t.Spec, machine, t.Input)
					if e != nil {
						modelprobe.Fail(&r, "error", "invalid_backend_configuration")
						return
					}
					defer func() {
						if !modelprobe.Cleanup(close) {
							modelprobe.Fail(&r, "error", "cleanup_failed")
						}
					}()
					check := l.CheckInstallation(targetCtx, domain.InstallationInput{WorkspaceDir: t.Input.WorkspaceDir, AmbientEnv: t.Input.AmbientEnv})
					if targetCtx.Err() != nil {
						modelprobe.Fail(&r, modelprobe.ContextStatus(targetCtx, "error"), "context_ended")
						return
					}
					if check.Status == domain.InstallationStatusFailed {
						for _, issue := range check.Issues {
							modelprobe.Issue(&r, targetCtx, issue)
						}
						if len(check.Issues) == 0 {
							modelprobe.Fail(&r, "error", "installation_check_failed")
						}
						return
					}
					r = l.ListModels(targetCtx, t.Input)
					if targetCtx.Err() != nil && r.Complete {
						modelprobe.Fail(&r, modelprobe.ContextStatus(targetCtx, "error"), "context_ended")
					}
					modelprobe.Sanitize(&r, modelprobe.EnvSecrets(t.Input.AmbientEnv))
				}()
			}
		}()
	}
	for i := range targets {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	report.Status = "ok"
	usable := false
	degraded := false
	for _, r := range report.Results {
		if len(r.Models) > 0 || r.Complete && r.Status == "empty" {
			usable = true
		}
		if !r.Complete || r.Status != "ok" && r.Status != "empty" {
			degraded = true
		}
	}
	if degraded {
		report.Status = "failed"
		if usable {
			report.Status = "partial"
		}
	}
	return report
}
