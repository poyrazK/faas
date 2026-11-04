package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// The automation agent step (ADR-20261027090010 §9). A step names an agent; its
// rendered input is the run request; the orchestrator admits one run per
// step attempt and parks the step until the run ends. The run's end arrives
// as a platform-reserved workflow event.

// workflowAgentEventPrefix reserves agent step results from customer events.
const workflowAgentEventPrefix = "workflow.agent."

var (
	// ErrWorkflowAgentStepInvalid is an agent step with options it cannot take.
	ErrWorkflowAgentStepInvalid = errors.New("workflow: invalid agent step")
	workflowAgentName           = regexp.MustCompile(fmt.Sprintf(`^[a-z][a-z0-9-]{0,%d}$`, AgentNameMaxLen-1))
	agentRepository             = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}/[A-Za-z0-9._-]{1,100}$`)
	agentCommit                 = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// WorkflowAgentRunInput is an agent step's rendered input: the run request.
// An omitted input runs the agent with the workflow run's input as inputs.
type WorkflowAgentRunInput struct {
	Version  int                `json:"version,omitempty"`
	Prompt   *string            `json:"prompt,omitempty"`
	Inputs   json.RawMessage    `json:"inputs,omitempty"`
	Checkout *AgentCheckout     `json:"checkout,omitempty"`
	GitHub   *AgentGitHubTarget `json:"github,omitempty"`
}

// WorkflowAgentStepOutput is an agent step's output once its run ended.
type WorkflowAgentStepOutput struct {
	RunID   string           `json:"run_id"`
	State   string           `json:"state"`
	Outcome *AgentRunOutcome `json:"outcome,omitempty"`
	// Result is the run's result.json when it succeeded and fits
	// WorkflowAgentStepResultMaxBytes; ResultOmitted marks a larger one.
	Result        json.RawMessage `json:"result,omitempty"`
	ResultOmitted bool            `json:"result_omitted,omitempty"`
}

// WorkflowAgentEventName is the event that delivers one attempt's run end.
// The attempt is part of the name, so a late end of an earlier attempt can
// never complete a retry.
func WorkflowAgentEventName(runID, stepName string, attempt int) string {
	return workflowAgentEventPrefix + uuid.NewSHA1(uuid.NameSpaceOID,
		[]byte("gregale-workflow-agent\x00"+runID+"\x00"+stepName+"\x00"+strconv.Itoa(attempt))).String()
}

// WorkflowUsesAgentSteps reports whether a definition runs any agent, so the
// agents capability can be enforced where definitions are accepted.
func WorkflowUsesAgentSteps(spec WorkflowSpec) bool {
	for _, step := range spec.Steps {
		if step.Agent != "" {
			return true
		}
	}
	return false
}

// WorkflowAgentStepTimeout is how long an agent step waits for its run.
func WorkflowAgentStepTimeout(spec WorkflowStepSpec) time.Duration {
	if spec.Timeout > 0 {
		return spec.Timeout
	}
	return WorkflowAgentStepDefaultTimeout
}

// validateWorkflowAgentStep allows an input template, dependencies, a guard
// and a timeout. Retries rerun the whole agent and are not offered yet; the
// run's own timeout bounds its work.
func validateWorkflowAgentStep(step WorkflowStepSpec, maxWait time.Duration) error {
	switch {
	case !workflowAgentName.MatchString(step.Agent):
		return fmt.Errorf("%w: agent must name an agent", ErrWorkflowAgentStepInvalid)
	case step.Method != "" || step.ManagedOperation || step.Retry != nil || step.OnTimeout != "" || step.OnFailure != "":
		return fmt.Errorf("%w: an agent step takes only input, depends_on, when and timeout", ErrWorkflowAgentStepInvalid)
	case step.Timeout < 0 || (step.Timeout > 0 && (step.Timeout < time.Minute || step.Timeout > min(maxWait, WorkflowAgentStepMaxTimeout))):
		return fmt.Errorf("%w: timeout must be between 1m and %s", ErrWorkflowAgentStepInvalid, min(maxWait, WorkflowAgentStepMaxTimeout))
	}
	return nil
}

// Validate checks a rendered agent step input against the same bounds the
// agent API applies to a run request; the store checks the rest at
// admission. It normalizes the commit to lower case.
func (in *WorkflowAgentRunInput) Validate() error {
	invalid := func(why string) error { return fmt.Errorf("%w: %s", ErrWorkflowAgentStepInvalid, why) }
	if in.Version < 0 {
		return invalid("version must be positive")
	}
	if p := in.Prompt; p != nil && (len(*p) > AgentRunPromptMaxBytes || !utf8.ValidString(*p) || strings.ContainsRune(*p, 0)) {
		return invalid(fmt.Sprintf("prompt must be UTF-8 text of at most %d bytes", AgentRunPromptMaxBytes))
	}
	if inputs := bytes.TrimSpace(in.Inputs); len(inputs) > 0 && !bytes.Equal(inputs, []byte("null")) {
		var object map[string]json.RawMessage
		if len(inputs) > AgentRunInputsMaxBytes || json.Unmarshal(inputs, &object) != nil || object == nil || bytes.Contains(inputs, []byte(`\u0000`)) {
			return invalid(fmt.Sprintf("inputs must be a JSON object of at most %d bytes", AgentRunInputsMaxBytes))
		}
	}
	if c := in.Checkout; c != nil {
		c.Commit = strings.ToLower(c.Commit)
		if !validAgentRepository(c.Repository) || !agentCommit.MatchString(c.Commit) || c.InstallationID < 0 {
			return invalid("checkout needs a repository as owner/name and a full commit SHA")
		}
	}
	if g := in.GitHub; g != nil && (!validAgentRepository(g.Repository) || g.Number <= 0 || g.InstallationID < 0) {
		return invalid("github needs a repository as owner/name and an issue or pull request number")
	}
	return nil
}

func validAgentRepository(repository string) bool {
	owner, name, _ := strings.Cut(repository, "/")
	return agentRepository.MatchString(repository) && owner != "." && owner != ".." && name != "." && name != ".."
}
