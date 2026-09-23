package store

import (
	"bytes"
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
	// Upgrade-only schema normalization at the commit point: a supported
	// nonnested execution always persists at the nonnested schema, so a
	// recovered schema-1 snapshot saved later becomes schema 2 without any
	// nesting fields. A nested execution persists at schema 3. This never
	// downgrades and otherwise leaves the flat wire format unchanged.
	if snapshot.Definition.HasNesting() {
		snapshot.SchemaVersion = domain.WorkflowSnapshotNestedSchemaVersion
	} else if snapshot.SchemaVersion < domain.WorkflowSnapshotNonNestedSchemaVersion {
		snapshot.SchemaVersion = domain.WorkflowSnapshotNonNestedSchemaVersion
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
	data, err := os.ReadFile(filepath.Join(dir, workflowSnapshotFile))
	if err != nil {
		return snapshot, err
	}
	// encoding/json silently keeps the last member when an object repeats a
	// key, so a damaged snapshot could otherwise hide a second current record
	// for the same loop and resolve to a latest-wins state. Reject any
	// duplicate object key before decoding, so duplicate/extra loop records and
	// other hand-edited corruption fail recovery instead of being chosen over.
	if path, key, found, err := firstDuplicateJSONKey(data); err != nil {
		return domain.WorkflowSnapshot{}, fmt.Errorf("workflow %s: corrupt snapshot: %w", executionID, err)
	} else if found {
		if path == "" {
			return domain.WorkflowSnapshot{}, fmt.Errorf("workflow %s: corrupt snapshot: duplicate top-level %q key; refusing to recover", executionID, key)
		}
		return domain.WorkflowSnapshot{}, fmt.Errorf("workflow %s: corrupt snapshot: duplicate %q record under %s; refusing to recover", executionID, key, path)
	}
	if err := json.Unmarshal(data, &snapshot); err != nil {
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
	if err := validateWorkflowSnapshot(&snapshot); err != nil {
		return domain.WorkflowSnapshot{}, fmt.Errorf("workflow %s: %w", executionID, err)
	}
	return snapshot, nil
}

// firstDuplicateJSONKey streams the raw document and reports the first member
// key that appears twice within the same JSON object, together with the dotted
// path of the containing object ("" for the root). encoding/json collapses
// duplicate keys to the last value, so this detection runs before decoding to
// fail recovery on a damaged or hand-edited snapshot instead of silently
// choosing a latest record. Token errors stop the traversal immediately so a
// malformed array cannot repeatedly retry the same invalid token.
func firstDuplicateJSONKey(data []byte) (path, key string, found bool, err error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return scanJSONValue(dec, "")
}

// scanJSONValue consumes exactly one JSON value from dec. For an object it
// checks its keys for repeats and recurses into each member value; for an array
// it recurses into each element; a scalar is simply consumed.
func scanJSONValue(dec *json.Decoder, path string) (string, string, bool, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", "", false, err
	}
	delim, isDelim := tok.(json.Delim)
	if !isDelim {
		return "", "", false, nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return "", "", false, err
			}
			k, _ := keyTok.(string)
			if seen[k] {
				return path, k, true, nil
			}
			seen[k] = true
			childPath := k
			if path != "" {
				childPath = path + "." + k
			}
			if p, dk, found, err := scanJSONValue(dec, childPath); err != nil || found {
				return p, dk, found, err
			}
		}
		if _, err := dec.Token(); err != nil {
			return "", "", false, err
		}
		return "", "", false, nil
	case '[':
		for dec.More() {
			if p, dk, found, err := scanJSONValue(dec, path); err != nil || found {
				return p, dk, found, err
			}
		}
		if _, err := dec.Token(); err != nil {
			return "", "", false, err
		}
		return "", "", false, nil
	}
	return "", "", false, nil
}

// validateWorkflowSnapshot fails closed on schema/definition mismatches and on
// malformed nested identities before a recovered execution is ever scheduled. It
// enforces the upgrade-only contract: nesting requires schema 3, schema 3
// requires nesting, and every schema-3 loop-owned loop execution and attempt
// carries a complete, ancestry-consistent iteration path while workflow-scope
// attempts carry none. Nonnested schema-1/2 snapshots are only checked for the
// absence of nesting, so their recovery behavior is unchanged.
func validateWorkflowSnapshot(snapshot *domain.WorkflowSnapshot) error {
	nested := snapshot.Definition.HasNesting()
	switch {
	case nested && snapshot.SchemaVersion < domain.WorkflowSnapshotNestedSchemaVersion:
		return fmt.Errorf("definition declares nested loops but snapshot uses schema %d; nested executions require schema %d", snapshot.SchemaVersion, domain.WorkflowSnapshotNestedSchemaVersion)
	case !nested && snapshot.SchemaVersion > domain.WorkflowSnapshotNonNestedSchemaVersion:
		return fmt.Errorf("snapshot uses schema %d but the definition has no loop parent; only nested executions use schema %d", snapshot.SchemaVersion, domain.WorkflowSnapshotNestedSchemaVersion)
	}
	if !nested {
		return nil
	}
	// Schema 3: exactly one loop-state record per declared loop, each with a
	// complete root-to-owner path whose last entry equals the local counter.
	for loopName := range snapshot.Definition.Loops {
		loop := snapshot.Loops[loopName]
		if loop == nil {
			return fmt.Errorf("missing loop execution record for declared loop %q", loopName)
		}
		if err := validateLoopPath(snapshot, loopName, loop.IterationPath, loop.Iteration); err != nil {
			return err
		}
	}
	for loopName := range snapshot.Loops {
		if _, declared := snapshot.Definition.Loops[loopName]; !declared {
			return fmt.Errorf("loop execution record %q is not a declared loop", loopName)
		}
	}
	// Every loop-owned attempt carries its owner's current-path shape; a
	// workflow-scope attempt must omit the field.
	for taskID, task := range snapshot.Tasks {
		owner := snapshot.Definition.Tasks[taskID].Loop
		if task == nil {
			continue
		}
		if owner == "" {
			for i := range task.Attempts {
				if task.Attempts[i].IterationPath != nil {
					return fmt.Errorf("workflow-scope task %q attempt %d must omit iteration_path", taskID, task.Attempts[i].Attempt)
				}
			}
			continue
		}
		for i := range task.Attempts {
			attempt := &task.Attempts[i]
			if len(attempt.IterationPath) == 0 {
				return fmt.Errorf("loop-owned task %q attempt %d is missing iteration_path", taskID, attempt.Attempt)
			}
			last := attempt.IterationPath[len(attempt.IterationPath)-1]
			if last.Loop != owner || last.Iteration != attempt.Iteration {
				return fmt.Errorf("loop-owned task %q attempt %d iteration_path %v disagrees with owner %q iteration %d", taskID, attempt.Attempt, attempt.IterationPath, owner, attempt.Iteration)
			}
			if ancestry := snapshot.Definition.LoopAncestry(owner); len(ancestry) != len(attempt.IterationPath) {
				return fmt.Errorf("loop-owned task %q attempt %d iteration_path does not match owner %q ancestry", taskID, attempt.Attempt, owner)
			}
			for j, entry := range attempt.IterationPath {
				if entry.Loop != ancestryName(snapshot.Definition.LoopAncestry(owner), j) {
					return fmt.Errorf("loop-owned task %q attempt %d iteration_path loop at position %d does not match ancestry", taskID, attempt.Attempt, j)
				}
				if entry.Iteration < 1 {
					return fmt.Errorf("loop-owned task %q attempt %d iteration_path has non-positive counter", taskID, attempt.Attempt)
				}
			}
		}
	}
	// State-consistency checks beyond path shape (the lifecycle recovery
	// contract): a schema-3 snapshot must not present contradictory live
	// contexts, so recovery fails closed instead of inferring a current
	// invocation from attempt order.
	//
	// A loop's current path must extend its parent's current path exactly.
	for loopName, loopDef := range snapshot.Definition.Loops {
		if loopDef.Parent == "" {
			continue
		}
		loop := snapshot.Loops[loopName]
		parent := snapshot.Loops[loopDef.Parent]
		if loop == nil || parent == nil || len(loop.IterationPath) == 0 {
			continue
		}
		if !domain.PathsEqual(parent.IterationPath, loop.IterationPath[:len(loop.IterationPath)-1]) {
			return fmt.Errorf("loop %q path %v is inconsistent with parent %q path %v", loopName, loop.IterationPath, loopDef.Parent, parent.IterationPath)
		}
	}
	// A done loop must not contain an unfinished child invocation.
	for loopName, loop := range snapshot.Loops {
		if loop == nil || loop.State != domain.WorkflowLoopDone {
			continue
		}
		for _, child := range snapshot.Definition.ChildLoops(loopName) {
			if cl := snapshot.Loops[child]; cl != nil && cl.State != domain.WorkflowLoopDone {
				return fmt.Errorf("loop %q is done but child loop %q is %s", loopName, child, cl.State)
			}
		}
	}
	// Nonterminal work and an unresolved latest interruption must belong to the
	// owner's current path. An interruption superseded by a later attempt stays
	// historical and is exempt even after an ancestor advances.
	for taskID, task := range snapshot.Tasks {
		owner := snapshot.Definition.Tasks[taskID].Loop
		if owner == "" || task == nil || len(task.Attempts) == 0 {
			continue
		}
		ownerLoop := snapshot.Loops[owner]
		if ownerLoop == nil {
			continue
		}
		for i := range task.Attempts {
			attempt := &task.Attempts[i]
			unresolvedInterruption := attempt.State == domain.WorkflowAttemptInterrupted && i == len(task.Attempts)-1
			live := !attempt.State.Committed() && attempt.State != domain.WorkflowAttemptInterrupted
			if !live && !unresolvedInterruption {
				continue
			}
			if !domain.PathsEqual(ownerLoop.IterationPath, attempt.IterationPath) {
				return fmt.Errorf("task %q attempt %d (%s) is outside owner %q current path %v", taskID, attempt.Attempt, attempt.State, owner, ownerLoop.IterationPath)
			}
		}
	}
	return nil
}

func ancestryName(ancestry []string, index int) string {
	if index < 0 || index >= len(ancestry) {
		return ""
	}
	return ancestry[index]
}

// validateLoopPath checks a loop execution's iteration path against its
// definition ancestry and local counter: the path runs root-to-owner, its loop
// names match the ancestry, its counters are positive, and its last entry equals
// the local iteration.
func validateLoopPath(snapshot *domain.WorkflowSnapshot, loopName string, path []domain.IterationEntry, localIteration int) error {
	ancestry := snapshot.Definition.LoopAncestry(loopName)
	if len(path) != len(ancestry) {
		return fmt.Errorf("loop %q iteration_path %v does not match ancestry %v", loopName, path, ancestry)
	}
	for i, entry := range path {
		if entry.Loop != ancestry[i] {
			return fmt.Errorf("loop %q iteration_path position %d names %q, expected %q", loopName, i, entry.Loop, ancestry[i])
		}
		if entry.Iteration < 1 {
			return fmt.Errorf("loop %q iteration_path has non-positive counter at position %d", loopName, i)
		}
	}
	if len(path) > 0 && path[len(path)-1].Iteration != localIteration {
		return fmt.Errorf("loop %q iteration_path last counter %d disagrees with local iteration %d", loopName, path[len(path)-1].Iteration, localIteration)
	}
	return nil
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
