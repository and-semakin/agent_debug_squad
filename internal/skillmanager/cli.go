package skillmanager

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
)

const Help = `Usage:
  agent-debug-squad skill install (--agent NAME ... | --path /absolute/skills/agent-debug-squad) [--adopt] [--allow-dev | --allow-downgrade] [--json]
  agent-debug-squad skill status [--agent NAME ... | --path DIR] [--json]
  agent-debug-squad skill sync [--path DIR] [--allow-dev | --allow-downgrade] [--json]
Hosts: codex, claude-code, cursor, opencode (aliases: opencode1, opencode2), zcode, kimi, kimi-cli.
--path is the exact skill directory, not its parent. Registration is explicit.
`

type repeat []string

func (a *repeat) String() string     { return "" }
func (a *repeat) Set(v string) error { *a = append(*a, v); return nil }
func Parse(args []string) (Options, error) {
	o := Options{JSON: HasJSON(args)}
	if len(args) == 0 {
		return o, invalidArg("Missing skill command")
	}
	o.Command = args[0]
	f := flag.NewFlagSet("skill "+o.Command, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	a := repeat{}
	f.Var(&a, "agent", "host preset (repeatable)")
	f.StringVar(&o.Path, "path", "", "exact skill directory")
	f.BoolVar(&o.Adopt, "adopt", false, "adopt selected manual copy")
	f.BoolVar(&o.AllowDev, "allow-dev", false, "allow development payload")
	f.BoolVar(&o.AllowDowngrade, "allow-downgrade", false, "allow older release")
	f.BoolVar(&o.JSON, "json", o.JSON, "JSON report")
	if e := f.Parse(args[1:]); e != nil || len(f.Args()) != 0 {
		return o, invalidArg("Invalid skill arguments; see skill --help")
	}
	o.Agents = a
	return o, nil
}
func Print(out io.Writer, r Report, jsonMode bool) error {
	if jsonMode {
		return json.NewEncoder(out).Encode(r)
	}
	fmt.Fprintf(out, "agent-debug-squad %s (%s)\n", readable(r.Program.Version), r.Program.BundleHash)
	if r.Binary != nil {
		fmt.Fprintf(out, "Program: %s -> %s: %s\n", readable(r.Binary.Before), readable(r.Binary.After), readable(r.Binary.Status))
		if r.Binary.Error != nil {
			fmt.Fprintf(out, "%s: %s\n", r.Binary.Error.Code, readable(r.Binary.Error.Message))
		}
	}
	if len(r.Destinations) == 0 {
		fmt.Fprintln(out, "No managed skills synchronized. Use skill install --agent NAME or --path DIR.")
	}
	for _, d := range r.Destinations {
		fmt.Fprintf(out, "%s: %s (%s)\n", readable(d.Path), d.State, d.Action)
		if d.Error != nil {
			fmt.Fprintf(out, "  %s: %s\n", d.Error.Code, readable(d.Error.Message))
		}
		if d.Backup != "" {
			fmt.Fprintf(out, "  Backup: %s\n", readable(d.Backup))
		}
		for _, p := range d.Preserved {
			fmt.Fprintf(out, "  Preserved unowned file: %s\n", readable(p))
		}
		if d.Reread {
			fmt.Fprintf(out, "  Skill updated. Please reread %s/SKILL.md before continuing; refresh or restart your agent if needed.\n", readable(d.Path))
		}
		if len(d.Agents) == 0 {
			fmt.Fprintln(out, "  Custom destination: agent discovery is your responsibility.")
		}
		if d.Retry != "" {
			fmt.Fprintf(out, "  Retry: %s\n", readable(d.Retry))
		}
	}
	for _, obs := range r.Observations {
		if obs.Code != "managed" {
			fmt.Fprintf(out, "Observed %s: %s; effective selection unknown.\n", readable(obs.Path), obs.Code)
		}
	}
	if r.Error != nil {
		fmt.Fprintf(out, "%s: %s\n", r.Error.Code, readable(r.Error.Message))
	}
	fmt.Fprintln(out, "Status:", r.Status)
	return nil
}
func Main(ctx context.Context, args []string, b Build, out io.Writer) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprint(out, Help)
		return 0
	}
	o, e := Parse(args)
	if e != nil {
		r := NewReport(b)
		r.Error = diag(e)
		r.Finish()
		Print(out, r, HasJSON(args))
		return 2
	}
	m, e := Default(b)
	if e != nil {
		r := NewReport(b)
		r.Error = diag(e)
		r.Finish()
		Print(out, r, o.JSON)
		return 1
	}
	r, code := m.Execute(ctx, o)
	if Print(out, r, o.JSON) != nil {
		return 1
	}
	return code
}
