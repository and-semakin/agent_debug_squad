package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/api"
	"github.com/and-semakin/agent_debug_squad/internal/config"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/judge"
	"github.com/and-semakin/agent_debug_squad/internal/modeldiscovery"
	"github.com/and-semakin/agent_debug_squad/internal/oneshot"
	"github.com/and-semakin/agent_debug_squad/internal/orchestrator"
	"github.com/and-semakin/agent_debug_squad/internal/selfupdate"
	"github.com/and-semakin/agent_debug_squad/internal/skillmanager"
	"github.com/and-semakin/agent_debug_squad/internal/store"
	"github.com/and-semakin/agent_debug_squad/internal/workflow"
)

const (
	serverShutdownTimeout = 5 * time.Second
	repositoryOwner       = "and-semakin"
	repositoryName        = "agent_debug_squad"
	noAutoUpdateEnv       = "AGENT_DEBUG_SQUAD_NO_AUTO_UPDATE"
)

var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

// usageError reports an argument error: the message is printed with CLI
// usage before exiting.
type usageError struct {
	code int
	err  error
}

func (e *usageError) Error() string { return e.err.Error() }

// exitError carries a final process exit code whose diagnostics were already
// printed by the command itself.
type exitError struct {
	code int
}

func (e *exitError) Error() string { return fmt.Sprintf("exit code %d", e.code) }

func main() {
	err := run(os.Args[1:])
	if err == nil {
		return
	}
	var ue *usageError
	var ee *exitError
	switch {
	case errors.As(err, &ue):
		fmt.Fprintln(os.Stderr, ue.err)
		usage(os.Stderr)
		os.Exit(ue.code)
	case errors.As(err, &ee):
		os.Exit(ee.code)
	default:
		fmt.Fprintln(os.Stderr, err)
		usage(os.Stderr)
		os.Exit(2)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing command")
	}
	switch args[0] {
	case "version":
		if len(args) != 1 {
			return fmt.Errorf("version does not accept arguments")
		}
		fmt.Printf("agent-debug-squad %s (commit %s, built %s)\n", version, commit, buildDate)
		return nil
	case "skill":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return &exitError{code: skillmanager.Main(ctx, args[1:], skillBuild(), os.Stdout)}
	case "update":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return update(ctx, args[1:])
	case "serve":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return serve(ctx, args[1:])
	case "models":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return &exitError{code: modeldiscovery.Main(ctx, args[1:], os.Stdout, os.Stderr)}
	case "run":
		code, argErr := oneshot.Main(args[1:], os.Stdout, os.Stderr)
		if argErr != nil {
			return &usageError{code: code, err: argErr}
		}
		return &exitError{code: code}
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func serve(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", "", "path to squad config file")
	noAutoUpdate := flags.Bool("no-auto-update", false, "disable the automatic update check")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return fmt.Errorf("--config is required")
	}
	continued := selfupdate.ConsumeContinuation(version)
	if !continued && !*noAutoUpdate && os.Getenv(noAutoUpdateEnv) != "1" && selfupdate.IsReleaseVersion(version) {
		report, updated, err := maintenanceRun(ctx)
		if err != nil {
			log.Printf("automatic maintenance failed: %v", err)
		} else {
			skillmanager.Print(os.Stderr, report, false)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if updated {
				if err := restartProcess(report.Binary.After); err != nil {
					return err
				}
			}
		}
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	// The machine backend settings file and the default judge key path both
	// resolve against the home directory; without it the loader would fall
	// back to the process working directory — often the workspace — which
	// the backend-config spec forbids.
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	machineBackends, err := config.LoadMachineBackends(home)
	if err != nil {
		return err
	}
	cfg.MachineBackends = machineBackends
	if names := machineBackends.Configured(); len(names) > 0 && cfg.LogLevel != domain.LogLevelQuiet {
		// Names only: proxy URLs may embed credentials and never belong in
		// logs.
		log.Printf("machine backend config loaded for: %s", strings.Join(names, ", "))
	}

	st := store.New(cfg)
	ownership, err := store.AcquireSessionOwnership(st.SessionDir())
	if err != nil {
		return err
	}
	defer func() {
		if err := ownership.Release(); err != nil {
			log.Printf("release session ownership: %v", err)
		}
	}()

	orch, err := orchestrator.New(ctx, cfg, st)
	if err != nil {
		return err
	}
	defer orch.Close()
	judgeClient, err := judge.Setup(cfg.Judge, cfg.Workflow, home, cfg.MachineBackends.JudgeProxyURL())
	if err != nil {
		return err
	}
	workflows := workflow.NewManager(cfg, st, orch)
	workflows.SetJudge(judgeClient)
	if err := workflows.Start(ctx); err != nil {
		return fmt.Errorf("recover workflows: %w", err)
	}
	handler := api.New(orch, workflows, cfg)

	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))
	if cfg.LogLevel != domain.LogLevelQuiet {
		log.Printf("listening on http://%s", addr)
	}
	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	errC := make(chan error, 1)
	go func() {
		errC <- srv.ListenAndServe()
	}()

	select {
	case err := <-errC:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), serverShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown server: %w", err)
		}
		err := <-errC
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		// Stop workflow scheduling first: it cancels owned workers and joins
		// them within the bound; anything unjoined stays uncertain in the
		// snapshot for the next start to recover.
		workflowCtx, workflowCancel := context.WithTimeout(context.Background(), serverShutdownTimeout)
		defer workflowCancel()
		if err := workflows.Stop(workflowCtx); err != nil {
			log.Printf("workflow shutdown: %v", err)
		}
		workerCtx, cancel := context.WithTimeout(context.Background(), serverShutdownTimeout)
		defer cancel()
		if err := orch.WaitForWorkers(workerCtx); err != nil {
			return fmt.Errorf("wait for workers: %w", err)
		}
		return nil
	}
}

var maintenanceRun = maintain
var restartProcess = restart

func skillBuild() skillmanager.Build {
	return skillmanager.Build{Version: version, Commit: commit, Date: buildDate}
}

func maintain(ctx context.Context) (skillmanager.Report, bool, error) {
	manager, err := skillmanager.Default(skillBuild())
	if err != nil {
		return skillmanager.Report{}, false, err
	}
	executable, err := os.Executable()
	if err != nil {
		return skillmanager.Report{}, false, err
	}
	u := selfupdate.Maintenance{Manager: manager, Executable: executable, Client: selfupdate.Client{Owner: repositoryOwner, Repository: repositoryName, Version: version}}
	report, updated := u.Run(ctx)
	return report, updated, nil
}
func update(ctx context.Context, args []string) error {
	jsonMode := skillmanager.HasJSON(args)
	if len(args) > 1 || len(args) == 1 && args[0] != "--json" && args[0] != "--json=true" {
		report := skillmanager.NewReport(skillBuild())
		report.Error = skillmanager.Failure("invalid_arguments", "Usage: agent-debug-squad update [--json]")
		report.Finish()
		skillmanager.Print(os.Stdout, report, jsonMode)
		return &exitError{code: 2}
	}
	report, _, err := maintain(ctx)
	if err != nil {
		report = skillmanager.NewReport(skillBuild())
		report.Error = skillmanager.Failure("maintenance_failed", "Cannot initialize maintenance; check filesystem access.")
		report.Finish()
	}
	if err := skillmanager.Print(os.Stdout, report, jsonMode); err != nil {
		return err
	}
	if report.Status != "ok" {
		return &exitError{code: 2}
	}
	return nil
}
func restart(nextVersion string) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate updated executable: %w", err)
	}
	log.Printf("restarting installed agent-debug-squad %s", nextVersion)
	// version is injected without v by GoReleaser; match the actual new process.
	markerVersion := strings.TrimPrefix(nextVersion, "v")
	if err := syscall.Exec(executable, os.Args, selfupdate.ContinuationEnvironment(markerVersion)); err != nil {
		return fmt.Errorf("restart updated executable: %w", err)
	}
	return nil
}

func usage(out *os.File) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  agent-debug-squad models [--all | --backend NAME] [--config FILE] [--workspace DIR] [--json]")
	fmt.Fprintln(out, "  agent-debug-squad serve --config squad.yaml [--no-auto-update]")
	fmt.Fprintln(out, "  agent-debug-squad run --config squad.yaml --request-id <id>")
	fmt.Fprintln(out, "  agent-debug-squad version")
	fmt.Fprintln(out, "  agent-debug-squad update [--json]")
	fmt.Fprint(out, skillmanager.Help)
}
