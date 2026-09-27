package modeldiscovery

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/and-semakin/agent_debug_squad/internal/config"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

type backendFlags []string

func (b *backendFlags) String() string     { return strings.Join(*b, ",") }
func (b *backendFlags) Set(s string) error { *b = append(*b, s); return nil }
func Main(ctx context.Context, args []string, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("models", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var o Options
	var backends backendFlags
	var jsonMode bool
	flags.BoolVar(&o.All, "all", false, "discover all real backends")
	flags.Var(&backends, "backend", "backend filter (repeatable)")
	flags.StringVar(&o.ConfigPath, "config", "", "optional squad configuration")
	flags.StringVar(&o.Workspace, "workspace", "", "workspace (default current directory)")
	flags.BoolVar(&o.IncludeHidden, "include-hidden", false, "request hidden models where supported")
	flags.BoolVar(&jsonMode, "json", false, "emit versioned JSON")
	overall := flags.Duration("timeout", 0, "overall discovery budget (default scales with target count)")
	perTarget := flags.Duration("backend-timeout", 30*time.Second, "per-target discovery budget")
	// Preserve the requested output mode even when an earlier flag is invalid.
	for _, arg := range args {
		if arg == "--json" || arg == "-json" || arg == "--json=true" || arg == "-json=true" {
			jsonMode = true
		}
	}
	requestedJSON := jsonMode
	fail := func(code string) int {
		r := domain.ModelCatalog{SchemaVersion: 1, GeneratedAt: time.Now().UTC(), Status: "failed", Results: []domain.ModelDiscoveryResult{}, Error: &domain.ModelCatalogError{Code: code, Message: "model discovery: " + code}}
		if requestedJSON || jsonMode {
			_ = json.NewEncoder(out).Encode(r)
		} else {
			fmt.Fprintln(stderr, r.Error.Message)
		}
		return 2
	}
	if e := flags.Parse(args); e != nil {
		if e == flag.ErrHelp {
			fmt.Fprintln(out, "Usage: agent-debug-squad models [--all | --backend NAME ...] [--config FILE] [--workspace DIR] [--json]")
			flags.SetOutput(out)
			flags.PrintDefaults()
			return 0
		}
		return fail("invalid_arguments")
	}
	o.Backends = backends
	explicitOverall := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "timeout" {
			explicitOverall = true
		}
	})
	if flags.NArg() != 0 || *perTarget <= 0 || explicitOverall && *overall <= 0 {
		return fail("invalid_arguments")
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return fail("invalid_configuration")
	}
	machine, e := config.LoadMachineBackends(home)
	if e != nil {
		return fail("invalid_configuration")
	}
	targets, e := Resolve(o, machine, os.Environ())
	if e != nil {
		return fail(e.Error())
	}
	computed, e := DefaultTimeout(len(targets), *perTarget)
	if e != nil {
		return fail("invalid_arguments")
	}
	if !explicitOverall {
		*overall = computed
	}
	r := Discover(ctx, targets, machine, *overall, *perTarget, nil)
	if jsonMode {
		if json.NewEncoder(out).Encode(r) != nil {
			return 1
		}
	} else {
		Human(out, r)
	}
	if ctx.Err() != nil {
		return 130
	}
	if r.Status != "ok" {
		return 1
	}
	return 0
}
func Human(w io.Writer, r domain.ModelCatalog) {
	// Quote untrusted fields so terminal escapes, newlines and delimiters are data.
	quote := strconv.Quote
	nullable := func(b *bool) string {
		if b == nil {
			return "unknown"
		}
		return strconv.FormatBool(*b)
	}
	for _, t := range r.Results {
		fmt.Fprintf(w, "%s %s agents=%s status=%s complete=%t\n", quote(t.Backend), quote(t.TargetID), quote(strings.Join(t.Agents, ",")), t.Status, t.Complete)
		for _, d := range t.Diagnostics {
			fmt.Fprintf(w, "  %s: %s\n", quote(d.Code), quote(d.Message))
		}
		for _, m := range t.Models {
			name := m.DisplayName
			if name == "" {
				name = m.ModelID
			}
			b, _ := json.Marshal(m.Selection.Options)
			fmt.Fprintf(w, "  provider=%s model=%s alias=%s name=%s configured=%s connected=%s supported=%t options=%s\n", quote(m.ProviderID), quote(m.ModelID), quote(m.Alias), quote(name), nullable(m.Configured), nullable(m.Connected), m.Selection.Supported, quote(string(b)))
			for _, p := range m.Parameters {
				fmt.Fprintf(w, "    %s values=%s default=%s option=%s\n", quote(p.NativeName), quote(strings.Join(p.Values, ",")), quote(p.Default), quote(p.SquadOption))
			}
		}
		for _, s := range t.Sources {
			fmt.Fprintf(w, "  source=%s retrieved=%s freshness=%s hidden=%s/%s complete=%t\n", quote(s.ID), s.RetrievedAt.UTC().Format(time.RFC3339), quote(s.Freshness.State), quote(s.HiddenPolicy.Requested), quote(s.HiddenPolicy.Applied), s.Complete)
		}
	}
	fmt.Fprintf(w, "%s; inference was not checked\n", r.Status)
}
