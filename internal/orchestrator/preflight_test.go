package orchestrator

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/store"
)

// TestPreflightAgentsDeduplicatesAndAttributes proves one pass checks an
// identical installation once with all affected agents attached, keeps
// distinct commands separate, and never checks unreferenced agents.
func TestPreflightAgentsDeduplicatesAndAttributes(t *testing.T) {
	cfg := testConfig(t, "A", "B", "C")
	missing := domain.AgentSpec{
		Name:    "Broken",
		Backend: "codex",
		StringOptions: map[string]string{
			"command": filepath.Join(t.TempDir(), "missing-codex"),
		},
	}
	broken2 := missing
	broken2.Name = "Broken2"
	cfg.Agents = append(cfg.Agents, missing, broken2)

	o, err := New(context.Background(), cfg, store.New(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()

	err = o.PreflightAgents(context.Background(), []string{"Broken", "Broken2"})
	var report *domain.PreflightError
	if !errors.As(err, &report) || report.Report == nil || len(report.Report.Issues) != 1 {
		t.Fatalf("want one aggregated issue, got %v", err)
	}
	issue := report.Report.Issues[0]
	if len(issue.Agents) != 2 || issue.Agents[0] != "Broken" || issue.Agents[1] != "Broken2" {
		t.Fatalf("issue must name both affected agents, got %+v", issue)
	}
	if len(issue.InstallationLinks) == 0 {
		t.Fatal("official installation links are required")
	}

	// Fake backends are always admitted without filesystem or network work.
	if err := o.PreflightAgents(context.Background(), []string{"A", "B", "C"}); err != nil {
		t.Fatalf("fake agents must pass: %v", err)
	}
}

func TestSubmitRunRejectsMissingInstallationBeforeRunCreation(t *testing.T) {
	cfg := testConfig(t, "Reviewer")
	cfg.Agents[0].Backend = "codex"
	cfg.Agents[0].StringOptions = map[string]string{
		"command": filepath.Join(t.TempDir(), "missing"),
	}
	o, err := New(context.Background(), cfg, store.New(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = o.SubmitRun(ctx, "Reviewer", "review", nil)
	var report *domain.PreflightError
	if !errors.As(err, &report) {
		t.Fatalf("want PreflightError, got %v", err)
	}
	runs, err := o.Runs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("no run record may exist for a rejected admission, got %d", len(runs))
	}
	transcript, err := o.Transcript(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(transcript) != 0 {
		t.Fatalf("no facilitator message may be appended for a rejected admission, got %d", len(transcript))
	}
}

func TestPreflightSkipsUnknownNames(t *testing.T) {
	cfg := testConfig(t, "Reviewer")
	o, err := New(context.Background(), cfg, store.New(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	if err := o.PreflightAgents(context.Background(), []string{"Ghost"}); err != nil {
		t.Fatalf("unknown names never block work: %v", err)
	}
}
