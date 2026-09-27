package adapters

import (
	"context"
	"fmt"
	"github.com/and-semakin/agent_debug_squad/internal/adapters/opencode"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// ModelLister never allocates an agent conversation.
type ModelLister interface {
	ListModels(context.Context, domain.ModelDiscoveryInput) domain.ModelDiscoveryResult
	CheckInstallation(context.Context, domain.InstallationInput) domain.InstallationResult
}

func NewModelLister(ctx context.Context, spec domain.AgentSpec, machine domain.MachineBackends, in domain.ModelDiscoveryInput) (ModelLister, func(), error) {
	if spec.Backend == "opencode" {
		r := opencode.NewRuntimeWithEnv(ctx, in.WorkspaceDir, machine.OpenCode, in.AmbientEnv)
		a, err := r.Adapter(spec)
		if err != nil {
			r.Close()
			return nil, func() {}, err
		}
		return a, r.Close, nil
	}
	a, err := New(spec)
	if err != nil {
		return nil, func() {}, err
	}
	l, ok := a.(ModelLister)
	if !ok {
		return nil, func() {}, fmt.Errorf("model discovery unsupported")
	}
	return l, func() {}, nil
}
