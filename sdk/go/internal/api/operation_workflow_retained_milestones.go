package api

// OperationWorkflowRetainedMilestoneRequirement selects a transaction-backed
// fact from a declared step of the same workflow contract.
type OperationWorkflowRetainedMilestoneRequirement struct {
	Operation string `json:"operation" yaml:"operation" toml:"operation"`
	Milestone string `json:"milestone" yaml:"milestone" toml:"milestone"`
}

// Verified means a matching app-reported fact remains retained in the selected
// ownership, subject, workflow instance and version. It is not external proof.
type OperationWorkflowRetainedMilestone struct {
	Requirement OperationWorkflowRetainedMilestoneRequirement `json:"requirement"`
	Status      string                                        `json:"status"`
	Evidence    *OperationWorkflowInstanceMilestoneRef        `json:"evidence,omitempty"`
}
