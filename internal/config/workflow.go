package config

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"gopkg.in/yaml.v3"
)

type rawWorkflow struct {
	Version             int                        `yaml:"version"`
	Name                string                     `yaml:"name"`
	MaxParallel         int                        `yaml:"max_parallel"`
	TaskTimeoutSeconds  *int                       `yaml:"task_timeout_seconds"`
	Loops               map[string]rawWorkflowLoop `yaml:"loops"`
	Tasks               map[string]rawWorkflowTask `yaml:"tasks"`
	ConfidenceThreshold *float64                   `yaml:"confidence_threshold"`
	OnUncertain         string                     `yaml:"on_uncertain"`
}

// rawWorkflowLoop accepts max_iterations plus the optional condition fields.
// The strict workflow-subtree decoder rejects any additional loop field.
type rawWorkflowLoop struct {
	MaxIterations int               `yaml:"max_iterations"`
	UntilTask     string            `yaml:"until_task"`
	OnVerdict     map[string]string `yaml:"on_verdict"`
	OnExhaustion  string            `yaml:"on_exhaustion"`
}

type rawWorkflowTask struct {
	Agent                     string            `yaml:"agent"`
	Prompt                    string            `yaml:"prompt"`
	Needs                     []string          `yaml:"needs"`
	Loop                      string            `yaml:"loop"`
	AllowedToFail             bool              `yaml:"allowed_to_fail"`
	MinSuccessfulDependencies int               `yaml:"min_successful_dependencies"`
	TimeoutSeconds            *int              `yaml:"timeout_seconds"`
	Verdicts                  map[string]string `yaml:"verdicts"`
}

// parseWorkflow extracts the optional `workflow` mapping from the document and
// returns the resolved definition with defaults applied. Only the workflow
// subtree is strict: unknown fields elsewhere keep today's behavior.
func parseWorkflow(data []byte) (*domain.WorkflowDefinition, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	root := documentMapping(&doc)
	if root == nil {
		return nil, nil
	}
	var workflowNode *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]
		if key.Tag != "!!str" || key.Value != "workflow" {
			continue
		}
		if workflowNode != nil {
			return nil, errors.New("duplicate workflow key")
		}
		if value.Tag == "!!null" {
			continue
		}
		if value.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("workflow must be a mapping, got %s", nodeKindName(value))
		}
		workflowNode = value
	}
	if workflowNode == nil {
		return nil, nil
	}

	encoded, err := yaml.Marshal(workflowNode)
	if err != nil {
		return nil, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(encoded))
	decoder.KnownFields(true)
	var raw rawWorkflow
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("workflow: %w", err)
	}

	def := domain.WorkflowDefinition{
		Version:            raw.Version,
		Name:               strings.TrimSpace(raw.Name),
		MaxParallel:        raw.MaxParallel,
		TaskTimeoutSeconds: domain.DefaultWorkflowTaskTimeoutSec,
		Tasks:              make(map[string]domain.WorkflowTaskDefinition, len(raw.Tasks)),
		OnUncertain:        strings.TrimSpace(raw.OnUncertain),
	}
	if raw.TaskTimeoutSeconds != nil {
		def.TaskTimeoutSeconds = *raw.TaskTimeoutSeconds
	}
	if raw.ConfidenceThreshold != nil {
		def.ConfidenceThreshold = *raw.ConfidenceThreshold
	}
	if len(raw.Loops) > 0 {
		// Copy only when loops are declared: a nil map keeps loopless
		// definitions marshaling byte-identically to pre-loop parsers.
		def.Loops = make(map[string]domain.WorkflowLoopDefinition, len(raw.Loops))
		for loopName, loop := range raw.Loops {
			definition := domain.WorkflowLoopDefinition{
				MaxIterations: loop.MaxIterations,
				UntilTask:     strings.TrimSpace(loop.UntilTask),
				OnExhaustion:  strings.TrimSpace(loop.OnExhaustion),
			}
			if loop.OnVerdict != nil {
				// Copy even when empty: an explicitly empty on_verdict map is
				// a validation error, not an absent declaration.
				onVerdict := make(map[string]string, len(loop.OnVerdict))
				for verdict, action := range loop.OnVerdict {
					onVerdict[verdict] = strings.TrimSpace(action)
				}
				definition.OnVerdict = onVerdict
			}
			def.Loops[loopName] = definition
		}
	}
	for taskID, task := range raw.Tasks {
		needs := append([]string(nil), task.Needs...)
		resolved := domain.WorkflowTaskDefinition{
			Agent:                     strings.TrimSpace(task.Agent),
			Prompt:                    task.Prompt,
			Needs:                     needs,
			Loop:                      strings.TrimSpace(task.Loop),
			AllowedToFail:             task.AllowedToFail,
			MinSuccessfulDependencies: task.MinSuccessfulDependencies,
		}
		if task.Verdicts != nil {
			// Copy even when empty: an explicitly empty verdict map is a
			// validation error, not an absent declaration.
			verdicts := make(map[string]string, len(task.Verdicts))
			for name, description := range task.Verdicts {
				verdicts[name] = description
			}
			resolved.Verdicts = verdicts
		}
		if task.TimeoutSeconds != nil {
			resolved.TimeoutSeconds = *task.TimeoutSeconds
		}
		def.Tasks[taskID] = resolved
	}
	return &def, nil
}

func documentMapping(doc *yaml.Node) *yaml.Node {
	node := doc
	if node != nil && node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return nil
		}
		node = node.Content[0]
	}
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	return node
}

func nodeKindName(node *yaml.Node) string {
	switch node.Kind {
	case yaml.SequenceNode:
		return "a sequence"
	case yaml.ScalarNode:
		return "a scalar"
	case yaml.AliasNode:
		return "an alias"
	default:
		return "another node kind"
	}
}

var workflowIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// ValidateWorkflowDefinition checks graph invariants and agent references
// before any execution is admitted.
func ValidateWorkflowDefinition(def domain.WorkflowDefinition, agents []domain.AgentSpec) error {
	if def.Version != domain.WorkflowDefinitionVersion {
		return fmt.Errorf("workflow version must be %d, got %d", domain.WorkflowDefinitionVersion, def.Version)
	}
	if def.Name == "" {
		return errors.New("workflow name is required")
	}
	if def.MaxParallel < 1 {
		return fmt.Errorf("workflow max_parallel must be a positive integer, got %d", def.MaxParallel)
	}
	if def.TaskTimeoutSeconds < 1 {
		return fmt.Errorf("workflow task_timeout_seconds must be a positive integer, got %d", def.TaskTimeoutSeconds)
	}
	if len(def.Tasks) == 0 {
		return errors.New("workflow tasks must not be empty")
	}
	switch def.OnUncertain {
	case "", domain.WorkflowOnUncertainNeedsAttention, domain.WorkflowOnUncertainError:
	default:
		return fmt.Errorf("workflow on_uncertain must be %q or %q, got %q (the former %q spelling is no longer supported; use %q)",
			domain.WorkflowOnUncertainNeedsAttention, domain.WorkflowOnUncertainError, def.OnUncertain, "hold", domain.WorkflowOnUncertainNeedsAttention)
	}
	if def.ConfidenceThreshold < 0 || def.ConfidenceThreshold > 1 {
		return fmt.Errorf("workflow confidence_threshold must be greater than 0 and at most 1, got %v", def.ConfidenceThreshold)
	}

	knownAgents := make(map[string]bool, len(agents))
	for _, agent := range agents {
		knownAgents[agent.Name] = true
	}

	taskIDs := sortedTaskIDs(def.Tasks)
	knownTasks := make(map[string]bool, len(def.Tasks))
	for _, taskID := range taskIDs {
		if !workflowIdentifierPattern.MatchString(taskID) {
			return fmt.Errorf("unsafe workflow task identifier %q: must match %s", taskID, workflowIdentifierPattern.String())
		}
		knownTasks[taskID] = true
	}

	loopNames := sortedLoopNames(def.Loops)
	for _, loopName := range loopNames {
		if !workflowIdentifierPattern.MatchString(loopName) {
			return loopErrorf(loopName, "unsafe loop name: must match %s", workflowIdentifierPattern.String())
		}
		if def.Loops[loopName].MaxIterations < 1 {
			return loopErrorf(loopName, "max_iterations is required and must be a positive integer, got %d", def.Loops[loopName].MaxIterations)
		}
	}

	usedAgents := map[string]string{}
	for _, taskID := range taskIDs {
		task := def.Tasks[taskID]
		if task.Agent == "" {
			return fmt.Errorf("workflow task %q: agent is required", taskID)
		}
		if !knownAgents[task.Agent] {
			return fmt.Errorf("workflow task %q references unknown agent %q", taskID, task.Agent)
		}
		if previous, conflict := usedAgents[task.Agent]; conflict {
			return fmt.Errorf("workflow tasks %q and %q both reference agent %q; declare distinct agent names to reuse one model", previous, taskID, task.Agent)
		}
		usedAgents[task.Agent] = taskID

		if strings.TrimSpace(task.Prompt) == "" {
			return fmt.Errorf("workflow task %q: prompt is required", taskID)
		}
		if task.Loop != "" {
			if _, declared := def.Loops[task.Loop]; !declared {
				return loopErrorf(task.Loop, "referenced by task %q but not declared; add it to the loops map", taskID)
			}
		}
		if task.TimeoutSeconds < 0 {
			return fmt.Errorf("workflow task %q: timeout_seconds must be a positive integer, got %d", taskID, task.TimeoutSeconds)
		}
		if task.Verdicts != nil {
			if len(task.Verdicts) < 2 {
				return fmt.Errorf("workflow task %q: verdicts must declare at least two options, got %d", taskID, len(task.Verdicts))
			}
			for verdictName := range task.Verdicts {
				if !workflowIdentifierPattern.MatchString(verdictName) {
					return fmt.Errorf("workflow task %q: unsafe verdict name %q: must match %s", taskID, verdictName, workflowIdentifierPattern.String())
				}
				if verdictName == domain.ReservedVerdictName {
					return fmt.Errorf("workflow task %q: verdict name %q is reserved for the below-threshold outcome", taskID, domain.ReservedVerdictName)
				}
			}
		}

		seenNeeds := map[string]bool{}
		for _, dep := range task.Needs {
			if dep == taskID {
				return fmt.Errorf("workflow task %q depends on itself", taskID)
			}
			if !knownTasks[dep] {
				return fmt.Errorf("workflow task %q references unknown dependency %q", taskID, dep)
			}
			if seenNeeds[dep] {
				return fmt.Errorf("workflow task %q repeats dependency %q", taskID, dep)
			}
			seenNeeds[dep] = true
		}
		if task.MinSuccessfulDependencies < 0 || task.MinSuccessfulDependencies > len(task.Needs) {
			return fmt.Errorf("workflow task %q: min_successful_dependencies must be between 0 and %d (the number of direct dependencies), got %d", taskID, len(task.Needs), task.MinSuccessfulDependencies)
		}
	}

	for _, loopName := range loopNames {
		if !loopHasMemberTasks(def, loopName) {
			return loopErrorf(loopName, "has no member tasks; label at least one task with loop: %s", loopName)
		}
	}
	for _, taskID := range taskIDs {
		task := def.Tasks[taskID]
		if task.Loop == "" {
			continue
		}
		for _, dep := range task.Needs {
			depLoop := def.Tasks[dep].Loop
			if depLoop != "" && depLoop != task.Loop {
				return loopErrorf(task.Loop, "task %q depends on task %q in loop %q; a loop body may depend only on its own tasks and on tasks outside all loops, so route the handoff through an outside task", taskID, dep, depLoop)
			}
		}
	}

	// Boundary cycles can also appear as raw cycles when body tasks chain;
	// detect them on the condensed graph first so the rejection names the loop.
	if loopName, cycle := findWorkflowBoundaryCycle(def); loopName != "" {
		return loopErrorf(loopName, "dependency cycle through the loop boundary: %s", cycle)
	}
	if cycle := findWorkflowCycle(def.Tasks); cycle != "" {
		return fmt.Errorf("workflow dependency cycle detected through task %q", cycle)
	}
	if err := validateLoopConditions(def); err != nil {
		return err
	}
	return nil
}

// loopErrorf formats every loop rejection uniformly: it names the offending
// loop, states the problem, and appends a short corrective YAML example so
// agents can repair a definition without additional context.
func loopErrorf(loopName, problem string, args ...any) error {
	return fmt.Errorf("loop %q: %s\nexample:\n  loops:\n    %s:\n      max_iterations: 3", loopName, fmt.Sprintf(problem, args...), loopName)
}

// conditionErrorf formats every loop-condition rejection uniformly: it names
// the offending loop (and task where relevant), states the problem, and
// appends a corrective YAML example showing the until_task/on_verdict surface.
func conditionErrorf(loopName, problem string, args ...any) error {
	return fmt.Errorf("loop %q: %s\nexample:\n  loops:\n    %s:\n      max_iterations: 3\n      until_task: review\n      on_verdict:\n        review_passed: break\n        issues_found: continue",
		loopName, fmt.Sprintf(problem, args...), loopName)
}

// validateLoopConditions enforces the until_task/on_verdict/on_exhaustion
// rules for every conditioned loop after the base graph and loop-membership
// checks have passed. Static loops (no condition fields) are skipped except
// for a stray on_exhaustion, which is rejected rather than silently ignored.
func validateLoopConditions(def domain.WorkflowDefinition) error {
	for _, loopName := range sortedLoopNames(def.Loops) {
		loop := def.Loops[loopName]
		untilTask := loop.UntilTask
		hasPair := untilTask != "" || loop.OnVerdict != nil
		if untilTask == "" && loop.OnVerdict != nil {
			return conditionErrorf(loopName, "declares on_verdict without until_task; name the condition task and map each of its declared verdicts")
		}
		if untilTask != "" && loop.OnVerdict == nil {
			return conditionErrorf(loopName, "declares until_task %q without on_verdict; map each verdict %q declares to break, continue, or needs_attention", untilTask, untilTask)
		}
		if loop.OnExhaustion != "" && !hasPair {
			return conditionErrorf(loopName, "declares on_exhaustion without a condition; on_exhaustion requires until_task and on_verdict")
		}
		if loop.OnExhaustion != "" {
			switch loop.OnExhaustion {
			case domain.WorkflowExhaustionNeedsAttention, domain.WorkflowExhaustionSucceed:
			default:
				return conditionErrorf(loopName, "on_exhaustion must be %q or %q, got %q",
					domain.WorkflowExhaustionNeedsAttention, domain.WorkflowExhaustionSucceed, loop.OnExhaustion)
			}
		}
		if untilTask == "" {
			continue
		}

		// until_task must be a member of this loop's body.
		untilDef, exists := def.Tasks[untilTask]
		if !exists || untilDef.Loop != loopName {
			return conditionErrorf(loopName, "until_task %q must be a member of the loop body (a task with loop: %s)", untilTask, loopName)
		}
		// until_task must declare verdicts.
		if len(untilDef.Verdicts) == 0 {
			return conditionErrorf(loopName, "until_task %q must declare verdicts to drive the condition", untilTask)
		}
		// until_task must be mandatory.
		if untilDef.AllowedToFail {
			return conditionErrorf(loopName, "until_task %q must not set allowed_to_fail: true; the condition task is mandatory, omit allowed_to_fail or set it to false", untilTask)
		}

		// on_verdict must exactly cover the declared verdict set.
		for verdictName := range untilDef.Verdicts {
			action, mapped := loop.OnVerdict[verdictName]
			if !mapped {
				return conditionErrorf(loopName, "on_verdict omits the declared verdict %q of until_task %q; map it to break, continue, or needs_attention", verdictName, untilTask)
			}
			switch action {
			case domain.WorkflowLoopActionBreak, domain.WorkflowLoopActionContinue, domain.WorkflowLoopActionNeedsAttention:
			default:
				return conditionErrorf(loopName, "on_verdict maps verdict %q of until_task %q to %q; the action must be break, continue, or needs_attention", verdictName, untilTask, action)
			}
		}
		for verdictName := range loop.OnVerdict {
			if _, declared := untilDef.Verdicts[verdictName]; !declared {
				return conditionErrorf(loopName, "on_verdict maps verdict %q which until_task %q does not declare", verdictName, untilTask)
			}
		}

		// until_task must be the unique body sink: every other body task must
		// reach it through same-loop needs edges. A singleton body qualifies.
		if uncovered, ok := findUncoveredBodyTask(def, loopName, untilTask); !ok {
			return conditionErrorf(loopName, "until_task %q must be the unique body sink, but body task %q does not reach it through same-loop dependencies; connect %q to %q", untilTask, uncovered, uncovered, untilTask)
		}
	}
	return nil
}

// findUncoveredBodyTask reports whether every body task other than untilTask
// is a direct or transitive predecessor of untilTask through same-loop needs
// edges — equivalently, whether untilTask (the loop's sink) transitively
// depends on every other body task. It returns (uncoveredTaskID, false) for the
// first body task that does not reach the sink; ok is true when the sink covers
// the whole body. A singleton body trivially qualifies.
func findUncoveredBodyTask(def domain.WorkflowDefinition, loopName, untilTask string) (string, bool) {
	// Walk untilTask's same-loop needs edges transitively: the collected set is
	// exactly the body tasks whose output flows into the sink. Any body task
	// outside this set is a branch that never reaches untilTask.
	reaches := map[string]bool{untilTask: true}
	queue := []string{untilTask}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, dep := range def.Tasks[current].Needs {
			if def.Tasks[dep].Loop != loopName || reaches[dep] {
				continue
			}
			reaches[dep] = true
			queue = append(queue, dep)
		}
	}
	for _, taskID := range sortedTaskIDs(def.Tasks) {
		if def.Tasks[taskID].Loop != loopName || taskID == untilTask {
			continue
		}
		if !reaches[taskID] {
			return taskID, false
		}
	}
	return "", true
}

func loopHasMemberTasks(def domain.WorkflowDefinition, loopName string) bool {
	for _, task := range def.Tasks {
		if task.Loop == loopName {
			return true
		}
	}
	return false
}

// findWorkflowBoundaryCycle collapses every loop body into a single node and
// looks for dependency cycles in the condensed graph; it runs before the raw
// graph check so boundary cycles are reported with the loop's name. Cycles
// that never touch a loop are left to findWorkflowCycle's task-named error,
// as are body-internal cycles (invisible once a body collapses to one node).
// The reported loop and cycle path are deterministic (sorted task/edge order).
func findWorkflowBoundaryCycle(def domain.WorkflowDefinition) (string, string) {
	node := func(taskID string) string {
		if loop := def.Tasks[taskID].Loop; loop != "" {
			return "loop:" + loop
		}
		return taskID
	}
	edges := map[string][]string{}
	nodes := map[string]bool{}
	for _, taskID := range sortedTaskIDs(def.Tasks) {
		from := node(taskID)
		nodes[from] = true
		for _, dep := range def.Tasks[taskID].Needs {
			to := node(dep)
			nodes[to] = true
			if from != to {
				edges[from] = append(edges[from], to)
			}
		}
	}
	sortedNodeNames := make([]string, 0, len(nodes))
	for name := range nodes {
		sortedNodeNames = append(sortedNodeNames, name)
	}
	sort.Strings(sortedNodeNames)
	for _, name := range sortedNodeNames {
		sort.Strings(edges[name])
	}

	const (
		visiting = 1
		visited  = 2
	)
	states := make(map[string]int, len(nodes))
	var stack []string
	var visit func(name string) []string
	visit = func(name string) []string {
		states[name] = visiting
		stack = append(stack, name)
		for _, dep := range edges[name] {
			switch states[dep] {
			case visiting:
				for i, open := range stack {
					if open == dep {
						return append(append([]string(nil), stack[i:]...), dep)
					}
				}
				return nil
			case visited:
				continue
			default:
				if cycle := visit(dep); cycle != nil {
					return cycle
				}
			}
		}
		stack = stack[:len(stack)-1]
		states[name] = visited
		return nil
	}
	for _, name := range sortedNodeNames {
		if states[name] != 0 {
			continue
		}
		if cycle := visit(name); cycle != nil {
			loops := make([]string, 0, len(cycle))
			for _, entry := range cycle {
				if strings.HasPrefix(entry, "loop:") {
					loops = append(loops, strings.TrimPrefix(entry, "loop:"))
				}
			}
			sort.Strings(loops)
			reported := strings.Join(cycle, " -> ")
			if len(loops) == 0 {
				// An outside-only cycle: not a boundary problem, let the raw
				// graph check report it with its task-named error.
				return "", reported
			}
			return loops[0], reported
		}
	}
	return "", ""
}

func sortedLoopNames(loops map[string]domain.WorkflowLoopDefinition) []string {
	names := make([]string, 0, len(loops))
	for name := range loops {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func findWorkflowCycle(tasks map[string]domain.WorkflowTaskDefinition) string {
	const (
		visiting = 1
		visited  = 2
	)
	states := make(map[string]int, len(tasks))
	var visit func(taskID string) string
	visit = func(taskID string) string {
		states[taskID] = visiting
		for _, dep := range tasks[taskID].Needs {
			switch states[dep] {
			case visiting:
				return dep
			case visited:
				continue
			default:
				if found := visit(dep); found != "" {
					return found
				}
			}
		}
		states[taskID] = visited
		return ""
	}
	for _, taskID := range sortedTaskIDs(tasks) {
		if states[taskID] != 0 {
			continue
		}
		if found := visit(taskID); found != "" {
			return found
		}
	}
	return ""
}

func sortedTaskIDs(tasks map[string]domain.WorkflowTaskDefinition) []string {
	ids := make([]string, 0, len(tasks))
	for taskID := range tasks {
		ids = append(ids, taskID)
	}
	sort.Strings(ids)
	return ids
}
