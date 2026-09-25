package oneshot

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failingWriter fails every write, standing in for a broken stdout pipe.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("simulated stdout failure") }

func TestMainSummaryWriteFailureExitsOne(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, freePort(t), simpleWorkflow(`
a:
  agent: alpha
  prompt: Do the work.
`))
	res := runMain(t, "--config", cfgPath, "--request-id", "req-write-fail")
	if res.code != exitSuccess {
		t.Fatalf("baseline exit=%d stderr=%s", res.code, res.stderr)
	}
	summary := decodeSummary(t, res.stdout)
	st := sessionStore(t, cfgPath)
	workflowDir, err := st.WorkflowDir(*summary.ExecutionID)
	if err != nil {
		t.Fatalf("workflow dir: %v", err)
	}

	// The snapshot stays committed; replay persists its summary next to it,
	// so make the workflow directory read-only to force the write failure.
	if err := os.Chmod(workflowDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(workflowDir, 0o755) })

	res = runMain(t, "--config", cfgPath, "--request-id", "req-write-fail")
	if res.code != exitFailure {
		t.Fatalf("exit=%d, want 1; stderr=%s", res.code, res.stderr)
	}
	replayed := decodeSummary(t, res.stdout)
	if replayed.SummaryPersisted {
		t.Fatalf("summary_persisted must be false: %+v", replayed)
	}
	if !strings.Contains(res.stderr, "persist run summary") {
		t.Fatalf("diagnostics: %s", res.stderr)
	}
}

func TestMainStdoutFailureExitsOne(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, freePort(t), simpleWorkflow(`
a:
  agent: alpha
  prompt: Do the work.
`))

	stderr := &bytes.Buffer{}
	code, usageErr := Main([]string{"--config", cfgPath, "--request-id", "req-stdout-fail"}, failingWriter{}, stderr)
	if code != exitFailure || usageErr != nil {
		t.Fatalf("exit=%d usageErr=%v stderr=%s", code, usageErr, stderr.String())
	}
	if !strings.Contains(stderr.String(), "write summary to stdout") {
		t.Fatalf("diagnostics: %s", stderr.String())
	}
	// The persisted summary remains available as evidence of the outcome.
	var stdout bytes.Buffer
	if _, usageErr := Main([]string{"--config", cfgPath, "--request-id", "req-stdout-fail"}, &stdout, stderr); usageErr != nil {
		t.Fatalf("replay: %v", usageErr)
	}
	summary := decodeSummary(t, stdout.String())
	st := sessionStore(t, cfgPath)
	workflowDir, err := st.WorkflowDir(*summary.ExecutionID)
	if err != nil {
		t.Fatalf("workflow dir: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(workflowDir, runSummaryFile))
	if err != nil {
		t.Fatalf("saved summary missing: %v", err)
	}
	var saved Summary
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("saved summary: %v", err)
	}
	if saved.WorkflowState == nil || *saved.WorkflowState != "succeeded" {
		t.Fatalf("saved summary state: %+v", saved)
	}
}
