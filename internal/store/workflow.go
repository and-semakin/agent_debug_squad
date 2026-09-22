package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

const workflowSnapshotFile = "workflow.json"

// SaveWorkflowSnapshot atomically replaces the authoritative snapshot of one
// execution. The caller serializes mutations; this write is the commit point.
func (s *Store) SaveWorkflowSnapshot(snapshot *domain.WorkflowSnapshot) error {
	dir, err := s.WorkflowDir(snapshot.ExecutionID)
	if err != nil {
		return err
	}
	return writeJSONAtomic(filepath.Join(dir, workflowSnapshotFile), snapshot)
}

// LoadWorkflowSnapshot reads one authoritative snapshot. Schema versions 1 and
// 2 are accepted; a version-1 snapshot predates loops and loads as a loopless
// execution. Unknown versions fail closed so a newer or damaged state directory
// is never misread. Compatibility is upgrade-only.
func (s *Store) LoadWorkflowSnapshot(executionID string) (domain.WorkflowSnapshot, error) {
	var snapshot domain.WorkflowSnapshot
	dir, err := s.WorkflowDir(executionID)
	if err != nil {
		return snapshot, err
	}
	if err := readJSON(filepath.Join(dir, workflowSnapshotFile), &snapshot); err != nil {
		return snapshot, err
	}
	if snapshot.SchemaVersion < domain.WorkflowSnapshotMinSchemaVersion || snapshot.SchemaVersion > domain.WorkflowSnapshotSchemaVersion {
		return domain.WorkflowSnapshot{}, fmt.Errorf(
			"workflow %s has unsupported schema version %d (supported: %d through %d); refusing to recover",
			executionID, snapshot.SchemaVersion, domain.WorkflowSnapshotMinSchemaVersion, domain.WorkflowSnapshotSchemaVersion,
		)
	}
	if snapshot.ExecutionID == "" {
		snapshot.ExecutionID = executionID
	}
	return snapshot, nil
}

// ListWorkflowExecutions returns execution IDs in creation (numeric) order.
func (s *Store) ListWorkflowExecutions() ([]string, error) {
	dir, err := s.workflowsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	type numbered struct {
		id  string
		num int64
	}
	found := make([]numbered, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		num, ok := parseWorkflowExecutionNumber(entry.Name())
		if !ok {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, entry.Name(), workflowSnapshotFile)); err != nil {
			continue
		}
		found = append(found, numbered{id: entry.Name(), num: num})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].num < found[j].num })
	ids := make([]string, 0, len(found))
	for _, item := range found {
		ids = append(ids, item.id)
	}
	return ids, nil
}

// NextWorkflowExecutionID allocates the next wf_NNNNNN identifier without
// creating the directory.
func (s *Store) NextWorkflowExecutionID() (string, error) {
	ids, err := s.ListWorkflowExecutions()
	if err != nil {
		return "", err
	}
	max := int64(0)
	for _, id := range ids {
		if num, ok := parseWorkflowExecutionNumber(id); ok && num > max {
			max = num
		}
	}
	return fmt.Sprintf("wf_%06d", max+1), nil
}

func parseWorkflowExecutionNumber(executionID string) (int64, bool) {
	value, ok := strings.CutPrefix(executionID, "wf_")
	if !ok {
		return 0, false
	}
	num, err := strconv.ParseInt(value, 10, 64)
	if err != nil || num < 1 {
		return 0, false
	}
	return num, true
}

func (s *Store) workflowsDir() (string, error) {
	sessionDir, err := s.sessionDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(sessionDir, "workflows"), nil
}

func (s *Store) WorkflowDir(executionID string) (string, error) {
	base, err := s.workflowsDir()
	if err != nil {
		return "", err
	}
	safe, err := safePathElement("execution_id", executionID)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, safe), nil
}

// WorkflowAttemptStatePath is where the workflow-owned runtime for one
// attempt keeps its agent state, isolating it from manual agent state.
func (s *Store) WorkflowAttemptStatePath(executionID, taskID string, attempt int) (string, error) {
	dir, err := s.workflowAttemptDir(executionID, taskID, attempt)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agent-state.json"), nil
}

// WriteWorkflowAttemptInput persists the exact prompt and input manifest of
// one attempt and returns their execution-relative paths.
func (s *Store) WriteWorkflowAttemptInput(executionID, taskID string, attempt int, prompt, manifest []byte) (promptPath, manifestPath string, err error) {
	dir, err := s.workflowAttemptDir(executionID, taskID, attempt)
	if err != nil {
		return "", "", err
	}
	promptPath = filepath.Join(dir, "prompt.txt")
	if err := writeFileAtomic(promptPath, prompt); err != nil {
		return "", "", fmt.Errorf("write prompt: %w", err)
	}
	manifestPath = filepath.Join(dir, "input-manifest.json")
	if err := writeFileAtomic(manifestPath, manifest); err != nil {
		return "", "", fmt.Errorf("write input manifest: %w", err)
	}
	promptRelative, err := s.toExecutionRelative(executionID, promptPath)
	if err != nil {
		return "", "", err
	}
	manifestRelative, err := s.toExecutionRelative(executionID, manifestPath)
	if err != nil {
		return "", "", err
	}
	return promptRelative, manifestRelative, nil
}

// WriteWorkflowResponse publishes the final response of a successful attempt
// with its size and SHA-256 so consumers can verify it before dispatch.
func (s *Store) WriteWorkflowResponse(executionID, taskID string, attempt int, content []byte) (path string, size int64, sha256hex string, err error) {
	dir, err := s.workflowAttemptDir(executionID, taskID, attempt)
	if err != nil {
		return "", 0, "", err
	}
	absolute := filepath.Join(dir, "response.txt")
	if err := writeFileAtomic(absolute, content); err != nil {
		return "", 0, "", fmt.Errorf("write response: %w", err)
	}
	relative, err := s.toExecutionRelative(executionID, absolute)
	if err != nil {
		return "", 0, "", err
	}
	sum := sha256.Sum256(content)
	return relative, int64(len(content)), hex.EncodeToString(sum[:]), nil
}

// WriteWorkflowDecision persists the raw judge decision of one attempt as a
// readable audit artifact next to its inputs and response.
func (s *Store) WriteWorkflowDecision(executionID, taskID string, attempt int, content []byte) (string, error) {
	dir, err := s.workflowAttemptDir(executionID, taskID, attempt)
	if err != nil {
		return "", err
	}
	absolute := filepath.Join(dir, "decision.json")
	if err := writeFileAtomic(absolute, content); err != nil {
		return "", fmt.Errorf("write decision: %w", err)
	}
	return s.toExecutionRelative(executionID, absolute)
}

// ReadWorkflowArtifact returns the bytes of an execution-relative artifact.
func (s *Store) ReadWorkflowArtifact(executionID, relativePath string) ([]byte, error) {
	absolute, err := s.resolveWorkflowArtifact(executionID, relativePath)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(absolute)
}

// VerifyWorkflowArtifact checks that a committed artifact still exists with
// the recorded size and hash. Missing or changed artifacts are errors.
func (s *Store) VerifyWorkflowArtifact(executionID, relativePath string, size int64, sha256hex string) error {
	content, err := s.ReadWorkflowArtifact(executionID, relativePath)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("committed result %s of workflow %s is missing", relativePath, executionID)
	}
	if err != nil {
		return err
	}
	if int64(len(content)) != size {
		return fmt.Errorf("committed result %s of workflow %s changed: size %d, expected %d", relativePath, executionID, len(content), size)
	}
	sum := sha256.Sum256(content)
	if hex.EncodeToString(sum[:]) != sha256hex {
		return fmt.Errorf("committed result %s of workflow %s changed: content hash mismatch", relativePath, executionID)
	}
	return nil
}

func (s *Store) AppendWorkflowEvent(executionID string, event domain.WorkflowControlEvent) error {
	dir, err := s.WorkflowDir(executionID)
	if err != nil {
		return err
	}
	return appendJSONLLine(filepath.Join(dir, "events.jsonl"), event)
}

func (s *Store) workflowAttemptDir(executionID, taskID string, attempt int) (string, error) {
	execDir, err := s.WorkflowDir(executionID)
	if err != nil {
		return "", err
	}
	safeTask, err := safePathElement("task_id", taskID)
	if err != nil {
		return "", err
	}
	if attempt < 1 {
		return "", fmt.Errorf("attempt number must be positive, got %d", attempt)
	}
	return filepath.Join(execDir, "tasks", safeTask, "attempts", strconv.Itoa(attempt)), nil
}

func (s *Store) resolveWorkflowArtifact(executionID, relativePath string) (string, error) {
	if relativePath == "" {
		return "", fmt.Errorf("artifact path is empty")
	}
	if filepath.IsAbs(relativePath) {
		return "", fmt.Errorf("artifact path %q must be execution-relative", relativePath)
	}
	execDir, err := s.WorkflowDir(executionID)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(execDir, relativePath)
	cleaned := filepath.Clean(joined)
	if cleaned != execDir && !strings.HasPrefix(cleaned, execDir+string(filepath.Separator)) {
		return "", fmt.Errorf("artifact path %q escapes workflow execution directory", relativePath)
	}
	return cleaned, nil
}

func (s *Store) toExecutionRelative(executionID, absolutePath string) (string, error) {
	execDir, err := s.WorkflowDir(executionID)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(execDir, absolutePath)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(relative), nil
}

func appendJSONLLine(path string, event any) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	return file.Sync()
}
