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
	"syscall"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/api"
	"github.com/and-semakin/agent_debug_squad/internal/config"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/orchestrator"
	"github.com/and-semakin/agent_debug_squad/internal/selfupdate"
	"github.com/and-semakin/agent_debug_squad/internal/store"
)

const (
	serverShutdownTimeout = 5 * time.Second
	updateCheckTimeout    = 5 * time.Second
	updateInstallTimeout  = 2 * time.Minute
	repositoryOwner       = "and-semakin"
	repositoryName        = "agent_debug_squad"
	noAutoUpdateEnv       = "AGENT_DEBUG_SQUAD_NO_AUTO_UPDATE"
)

var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
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
	case "update":
		if len(args) != 1 {
			return fmt.Errorf("update does not accept arguments")
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return update(ctx)
	case "serve":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return serve(ctx, args[1:])
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
	if !*noAutoUpdate && os.Getenv(noAutoUpdateEnv) != "1" && selfupdate.IsReleaseVersion(version) {
		updated, err := installUpdate(ctx)
		if err != nil {
			log.Printf("automatic update skipped: %v", err)
		} else if updated {
			if err := restart(); err != nil {
				log.Printf("automatic restart skipped: %v", err)
			}
		}
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	st := store.New(cfg)
	orch, err := orchestrator.New(ctx, cfg, st)
	if err != nil {
		return err
	}
	handler := api.New(orch, cfg)

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
		workerCtx, cancel := context.WithTimeout(context.Background(), serverShutdownTimeout)
		defer cancel()
		if err := orch.WaitForWorkers(workerCtx); err != nil {
			return fmt.Errorf("wait for workers: %w", err)
		}
		return nil
	}
}

func update(ctx context.Context) error {
	if !selfupdate.IsReleaseVersion(version) {
		return fmt.Errorf("self-update is unavailable in development build %q", version)
	}
	updated, err := installUpdate(ctx)
	if err != nil {
		return err
	}
	if !updated {
		fmt.Printf("agent-debug-squad %s is already up to date\n", version)
	}
	return nil
}

func installUpdate(ctx context.Context) (bool, error) {
	client := selfupdate.Client{
		Owner:      repositoryOwner,
		Repository: repositoryName,
		Version:    version,
	}
	checkCtx, cancelCheck := context.WithTimeout(ctx, updateCheckTimeout)
	release, err := client.Check(checkCtx)
	cancelCheck()
	if err != nil {
		return false, err
	}
	if release == nil {
		return false, nil
	}

	executable, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("locate current executable: %w", err)
	}
	log.Printf("updating agent-debug-squad from %s to %s", version, release.Version)
	installCtx, cancelInstall := context.WithTimeout(ctx, updateInstallTimeout)
	err = client.Install(installCtx, *release, executable)
	cancelInstall()
	if err != nil {
		return false, err
	}
	fmt.Printf("updated agent-debug-squad from %s to %s\n", version, release.Version)
	return true, nil
}

func restart() error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate updated executable: %w", err)
	}
	log.Printf("restarting agent-debug-squad %s with the original arguments", version)
	if err := syscall.Exec(executable, os.Args, os.Environ()); err != nil {
		return fmt.Errorf("restart updated executable: %w", err)
	}
	return nil
}

func usage(out *os.File) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  agent-debug-squad serve --config squad.yaml [--no-auto-update]")
	fmt.Fprintln(out, "  agent-debug-squad version")
	fmt.Fprintln(out, "  agent-debug-squad update")
}
