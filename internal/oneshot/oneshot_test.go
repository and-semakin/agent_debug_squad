package oneshot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/store"
)

// freePort returns an unused loopback TCP port for the config file.
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

func indentLines(text string, spaces int) string {
	pad := strings.Repeat(" ", spaces)
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, line := range lines {
		lines[i] = pad + line
	}
	return strings.Join(lines, "\n")
}

func writeConfig(t *testing.T, dir string, port int, workflowBody string) string {
	t.Helper()
	path := filepath.Join(dir, "squad.yaml")
	body := fmt.Sprintf(`session_name: oneshot-test
workspace_dir: %s
state_dir_name: .agent-debug-squad
host: 127.0.0.1
port: %d
defaults:
  yolo: true
agents:
  - name: alpha
    backend: fake
    startup_prompt: You are alpha.
    options:
      delay_ms: "40"
%s
`, dir, port, workflowBody)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func simpleWorkflow(tasks string) string {
	return fmt.Sprintf(`workflow:
  version: 1
  name: oneshot-test
  max_parallel: 2
  task_timeout_seconds: 30
  tasks:
%s`, indentLines(tasks, 4))
}

type runResult struct {
	code     int
	usageErr error
	stdout   string
	stderr   string
}

func runMain(t *testing.T, args ...string) runResult {
	t.Helper()
	isolateHome(t)
	var stdout, stderr bytes.Buffer
	code, usageErr := Main(args, &stdout, &stderr)
	return runResult{code: code, usageErr: usageErr, stdout: stdout.String(), stderr: stderr.String()}
}

func decodeSummary(t *testing.T, raw string) Summary {
	t.Helper()
	var summary Summary
	if err := json.Unmarshal([]byte(raw), &summary); err != nil {
		t.Fatalf("decode summary %q: %v", raw, err)
	}
	return summary
}

func summaryFile(t *testing.T, dir string) (string, []byte) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".agent-debug-squad", "sessions", "*", "workflows", "wf_*", runSummaryFile))
	if err != nil || len(matches) != 1 {
		t.Fatalf("run summary file: matches=%v err=%v", matches, err)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	return matches[0], data
}

func TestMainEndToEndSucceededAndReplay(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, freePort(t), simpleWorkflow(`
a:
  agent: alpha
  prompt: Do the work.
`))

	res := runMain(t, "--config", cfgPath, "--request-id", "req-e2e")
	if res.code != exitSuccess || res.usageErr != nil {
		t.Fatalf("exit=%d usageErr=%v stderr=%s", res.code, res.usageErr, res.stderr)
	}
	summary := decodeSummary(t, res.stdout)
	if summary.SummaryVersion != 1 {
		t.Fatalf("summary version = %d", summary.SummaryVersion)
	}
	if summary.ExecutionID == nil || summary.WorkflowState == nil || *summary.WorkflowState != "succeeded" {
		t.Fatalf("summary identity/state: %+v", summary)
	}
	if summary.ExitCode != exitSuccess || summary.ExitReason != ReasonTerminalOutcome {
		t.Fatalf("exit fields: %+v", summary)
	}
	if !summary.SummaryPersisted || summary.Cleanup.Status != CleanupComplete {
		t.Fatalf("cleanup fields: %+v", summary)
	}
	if summary.TaskCounts.Succeeded != 1 {
		t.Fatalf("counts: %+v", summary.TaskCounts)
	}
	_, data := summaryFile(t, dir)
	var fileSummary Summary
	if err := json.Unmarshal(data, &fileSummary); err != nil {
		t.Fatalf("file summary: %v", err)
	}
	if fileSummary.ExecutionID == nil || *fileSummary.ExecutionID != *summary.ExecutionID {
		t.Fatalf("file summary identity mismatch")
	}

	// Replay reports the same outcome without touching the execution.
	st := sessionStore(t, cfgPath)
	snapshot, err := st.LoadWorkflowSnapshot(*summary.ExecutionID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	res = runMain(t, "--config", cfgPath, "--request-id", "req-e2e")
	if res.code != exitSuccess {
		t.Fatalf("replay exit=%d stderr=%s", res.code, res.stderr)
	}
	replayed := decodeSummary(t, res.stdout)
	if replayed.ExecutionID == nil || *replayed.ExecutionID != *summary.ExecutionID {
		t.Fatalf("replay selected a different execution: %+v", replayed)
	}
	after, err := st.LoadWorkflowSnapshot(*summary.ExecutionID)
	if err != nil {
		t.Fatalf("reload snapshot: %v", err)
	}
	if after.Revision != snapshot.Revision || len(after.Tasks["a"].Attempts) != 1 {
		t.Fatalf("replay mutated the execution: revision %d -> %d", snapshot.Revision, after.Revision)
	}
}

func TestMainCompletedWithErrorsExitsZero(t *testing.T) {
	dir := t.TempDir()
	// The task exceeds its timeout, but the failure is tolerated.
	cfgPath := writeConfig(t, dir, freePort(t), simpleWorkflow(`
slow:
  agent: alpha
  prompt: Take your time.
  allowed_to_fail: true
  timeout_seconds: 1
`))
	// Give the agent a slow delay so the timeout triggers.
	overrideDelay(t, cfgPath, "2000")

	res := runMain(t, "--config", cfgPath, "--request-id", "req-cwe")
	if res.code != exitSuccess || res.usageErr != nil {
		t.Fatalf("exit=%d usageErr=%v stderr=%s", res.code, res.usageErr, res.stderr)
	}
	summary := decodeSummary(t, res.stdout)
	if summary.WorkflowState == nil || *summary.WorkflowState != "completed_with_errors" {
		t.Fatalf("state: %+v", summary)
	}
	if len(summary.FailedBlocked) != 1 || summary.FailedBlocked[0].TaskID != "slow" {
		t.Fatalf("failed tasks: %+v", summary.FailedBlocked)
	}
}

func TestMainFailedWorkflowExitsOne(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, freePort(t), simpleWorkflow(`
mandatory:
  agent: alpha
  prompt: Do the work.
  timeout_seconds: 1
`))
	overrideDelay(t, cfgPath, "2000")

	res := runMain(t, "--config", cfgPath, "--request-id", "req-failed")
	if res.code != exitFailure || res.usageErr != nil {
		t.Fatalf("exit=%d usageErr=%v stderr=%s", res.code, res.usageErr, res.stderr)
	}
	summary := decodeSummary(t, res.stdout)
	if summary.WorkflowState == nil || *summary.WorkflowState != "failed" {
		t.Fatalf("state: %+v", summary)
	}
	if summary.ExitCode != exitFailure {
		t.Fatalf("exit code: %+v", summary)
	}
}

func TestMainRequestFingerprintConflictExitsTwo(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, freePort(t), simpleWorkflow(`
a:
  agent: alpha
  prompt: Original prompt.
`))
	res := runMain(t, "--config", cfgPath, "--request-id", "req-conflict")
	if res.code != exitSuccess {
		t.Fatalf("initial run exit=%d stderr=%s", res.code, res.stderr)
	}

	edited := strings.Replace(cfgBody(t, cfgPath), "Original prompt.", "Changed prompt.", 1)
	if err := os.WriteFile(cfgPath, []byte(edited), 0o644); err != nil {
		t.Fatalf("edit config: %v", err)
	}
	res = runMain(t, "--config", cfgPath, "--request-id", "req-conflict")
	if res.code != exitStartupFailure || res.usageErr != nil {
		t.Fatalf("conflict exit=%d usageErr=%v stderr=%s", res.code, res.usageErr, res.stderr)
	}
	if !strings.Contains(res.stderr, "different resolved definition") {
		t.Fatalf("conflict diagnostics: %s", res.stderr)
	}
}

func TestMainForeignNonterminalRejected(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, freePort(t), simpleWorkflow(`
a:
  agent: alpha
  prompt: Do the work.
`))

	// Leave a nonterminal execution behind, as a crashed owner would.
	blockOnInterruptedExecution(t, cfgPath, "req-blocked")

	res := runMain(t, "--config", cfgPath, "--request-id", "req-other")
	if res.code != exitStartupFailure {
		t.Fatalf("exit=%d stderr=%s", res.code, res.stderr)
	}
	if !strings.Contains(res.stderr, "cannot create a new execution") {
		t.Fatalf("diagnostics: %s", res.stderr)
	}
}

func TestMainCorruptSnapshotExitsTwo(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, freePort(t), simpleWorkflow(`
a:
  agent: alpha
  prompt: Do the work.
`))
	st := sessionStore(t, cfgPath)
	wfDir, err := st.WorkflowDir("wf_000001")
	if err != nil {
		t.Fatalf("workflow dir: %v", err)
	}
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "workflow.json"), []byte("{corrupt"), 0o644); err != nil {
		t.Fatalf("write corrupt snapshot: %v", err)
	}

	res := runMain(t, "--config", cfgPath, "--request-id", "req-corrupt")
	if res.code != exitStartupFailure {
		t.Fatalf("exit=%d stderr=%s", res.code, res.stderr)
	}
	if !strings.Contains(res.stderr, "wf_000001") {
		t.Fatalf("diagnostics must name the damaged execution: %s", res.stderr)
	}
}

func TestMainMissingWorkflowExitsTwo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "squad.yaml")
	body := fmt.Sprintf(`session_name: oneshot-test
workspace_dir: %s
state_dir_name: .agent-debug-squad
host: 127.0.0.1
port: %d
agents:
  - name: alpha
    backend: fake
    startup_prompt: You are alpha.
`, dir, freePort(t))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	res := runMain(t, "--config", path, "--request-id", "req-no-wf")
	if res.code != exitStartupFailure || res.usageErr != nil {
		t.Fatalf("exit=%d usageErr=%v", res.code, res.usageErr)
	}
	if !strings.Contains(res.stderr, "no workflow is configured") {
		t.Fatalf("diagnostics: %s", res.stderr)
	}
}

func TestMainArgumentErrorsReportUsage(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, freePort(t), simpleWorkflow(`
a:
  agent: alpha
  prompt: Do the work.
`))

	res := runMain(t, "--config", cfgPath)
	if res.code != exitStartupFailure || res.usageErr == nil {
		t.Fatalf("missing request id: exit=%d usageErr=%v", res.code, res.usageErr)
	}
	res = runMain(t, "--config", cfgPath, "--request-id", "   \t\n ")
	if res.code != exitStartupFailure || res.usageErr == nil {
		t.Fatalf("whitespace request id: exit=%d usageErr=%v", res.code, res.usageErr)
	}
	res = runMain(t, "--config", cfgPath, "--request-id", "req-x", "positional")
	if res.code != exitStartupFailure || res.usageErr == nil {
		t.Fatalf("positional argument: exit=%d usageErr=%v", res.code, res.usageErr)
	}
	res = runMain(t, "--config", cfgPath, "--request-id", "req-x", "--unknown")
	if res.code != exitStartupFailure || res.usageErr == nil {
		t.Fatalf("unknown flag: exit=%d usageErr=%v", res.code, res.usageErr)
	}
}

func TestMainOwnershipConflictExitsTwo(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, freePort(t), simpleWorkflow(`
a:
  agent: alpha
  prompt: Do the work.
`))
	st := sessionStore(t, cfgPath)
	ownership, err := store.AcquireSessionOwnership(st.SessionDir())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer func() { _ = ownership.Release() }()

	res := runMain(t, "--config", cfgPath, "--request-id", "req-owned")
	if res.code != exitStartupFailure {
		t.Fatalf("exit=%d stderr=%s", res.code, res.stderr)
	}
	if !strings.Contains(strings.ToLower(res.stderr), "ownership") {
		t.Fatalf("diagnostics: %s", res.stderr)
	}
}

func TestMainPortConflictExitsTwoWithoutTouchingOwner(t *testing.T) {
	dir := t.TempDir()
	port := freePort(t)
	cfgPath := writeConfig(t, dir, port, simpleWorkflow(`
a:
  agent: alpha
  prompt: Do the work.
`))
	owner, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
	if err != nil {
		t.Fatalf("occupy port: %v", err)
	}
	defer func() { _ = owner.Close() }()

	res := runMain(t, "--config", cfgPath, "--request-id", "req-port")
	if res.code != exitStartupFailure {
		t.Fatalf("exit=%d stderr=%s", res.code, res.stderr)
	}
	if !strings.Contains(res.stderr, "bind control listener") {
		t.Fatalf("diagnostics: %s", res.stderr)
	}
	// The port owner must be untouched: the listener still works.
	probe, err := net.Dial("tcp", owner.Addr().String())
	if err != nil {
		t.Fatalf("port owner was disturbed: %v", err)
	}
	_ = probe.Close()
}

func TestMainReplayDetectsMissingOutput(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, freePort(t), simpleWorkflow(`
a:
  agent: alpha
  prompt: Do the work.
`))
	res := runMain(t, "--config", cfgPath, "--request-id", "req-missing")
	if res.code != exitSuccess {
		t.Fatalf("initial exit=%d stderr=%s", res.code, res.stderr)
	}
	summary := decodeSummary(t, res.stdout)
	st := sessionStore(t, cfgPath)
	snapshot, err := st.LoadWorkflowSnapshot(*summary.ExecutionID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	resultPath := snapshot.Tasks["a"].Attempts[0].ResultPath
	workflowDir, err := st.WorkflowDir(*summary.ExecutionID)
	if err != nil {
		t.Fatalf("workflow dir: %v", err)
	}
	if err := os.Remove(filepath.Join(workflowDir, filepath.FromSlash(resultPath))); err != nil {
		t.Fatalf("remove response: %v", err)
	}

	res = runMain(t, "--config", cfgPath, "--request-id", "req-missing")
	if res.code != exitFailure {
		t.Fatalf("replay exit=%d stderr=%s", res.code, res.stderr)
	}
	if !strings.Contains(res.stderr, "missing") {
		t.Fatalf("diagnostics: %s", res.stderr)
	}
	after, err := st.LoadWorkflowSnapshot(*summary.ExecutionID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.State != domain.WorkflowSucceeded || len(after.Tasks["a"].Attempts) != len(snapshot.Tasks["a"].Attempts) {
		t.Fatalf("replay rewrote the terminal execution: %+v", after.State)
	}
}
