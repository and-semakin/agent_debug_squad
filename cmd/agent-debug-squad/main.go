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

	"github.com/andrey/agent-debug-squad/internal/api"
	"github.com/andrey/agent-debug-squad/internal/config"
	"github.com/andrey/agent-debug-squad/internal/orchestrator"
	"github.com/andrey/agent-debug-squad/internal/store"
)

const serverShutdownTimeout = 5 * time.Second

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
	if args[0] != "serve" {
		return fmt.Errorf("unknown command %q", args[0])
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serve(ctx, args[1:])
}

func serve(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", "", "path to squad config file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return fmt.Errorf("--config is required")
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
	log.Printf("listening on http://%s", addr)
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

func usage(out *os.File) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  agent-debug-squad serve --config squad.yaml")
}
