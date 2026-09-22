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
	Tasks               map[string]rawWorkflowTask `yaml:"tasks"`
	ConfidenceThreshold *float64                   `yaml:"confidence_threshold"`
	OnUncertain         string                     `yaml:"on_uncertain"`
}

type rawWorkflowTask struct {
	Agent                     string            `yaml:"agent"`
	Prompt                    string            `yaml:"prompt"`
	Needs                     []string          `yaml:"needs"`
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
	for taskID, task := range raw.Tasks {
		needs := append([]string(nil), task.Needs...)
		resolved := domain.WorkflowTaskDefinition{
			Agent:                     strings.TrimSpace(task.Agent),
			Prompt:                    task.Prompt,
			Needs:                     needs,
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
	if def.Version != domain.WorkflowSchemaVersion {
		return fmt.Errorf("workflow version must be %d, got %d", domain.WorkflowSchemaVersion, def.Version)
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
	case "", domain.WorkflowOnUncertainHold, domain.WorkflowOnUncertainError:
	default:
		return fmt.Errorf("workflow on_uncertain must be %q or %q, got %q", domain.WorkflowOnUncertainHold, domain.WorkflowOnUncertainError, def.OnUncertain)
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

	if cycle := findWorkflowCycle(def.Tasks); cycle != "" {
		return fmt.Errorf("workflow dependency cycle detected through task %q", cycle)
	}
	return nil
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
