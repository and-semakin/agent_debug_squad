package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/andrey/agent-debug-squad/internal/api"
	"github.com/andrey/agent-debug-squad/internal/config"
	"github.com/andrey/agent-debug-squad/internal/orchestrator"
	"github.com/andrey/agent-debug-squad/internal/store"
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
	if args[0] != "serve" {
		return fmt.Errorf("unknown command %q", args[0])
	}
	return serve(args[1:])
}

func serve(args []string) error {
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
	orch, err := orchestrator.New(context.Background(), cfg, st)
	if err != nil {
		return err
	}
	srv := api.New(orch, cfg)

	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))
	log.Printf("listening on http://%s", addr)
	return http.ListenAndServe(addr, srv)
}

func usage(out *os.File) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  agent-debug-squad serve --config squad.yaml")
}
