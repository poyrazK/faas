package api

const OperationWorkflowStateStaleAfterMaxSeconds int64 = 10 * 365 * 24 * 60 * 60

type OperationWorkflowSpec struct {
	Workflow               string                        `json:"workflow"`
	Title                  string                        `json:"title"`
	States                 []string                      `json:"states,omitempty"`
	TerminalStates         []string                      `json:"terminal_states,omitempty"`
	StateStaleAfterSeconds map[string]int64              `json:"state_stale_after_seconds,omitempty"`
	Transitions            []OperationWorkflowTransition `json:"transitions,omitempty"`
	Step                   string                        `json:"step"`
	Label                  string                        `json:"label"`
	Milestone              string                        `json:"milestone"`
	InstanceIDFrom         string                        `json:"instance_id_from,omitempty"`
	InstanceID             string                        `json:"instance_id,omitempty"`
	Position               int                           `json:"position"`
}

type OperationWorkflowTransition struct {
	From string `json:"from"`
	To   string `json:"to"`
}
