package dashboard

import "github.com/onebox-faas/faas/pkg/api"

// CustomerOperationsData carries retained business metadata and declared public facts. Input,
// result bodies, artifact storage locations and execution authority stay
// behind the existing explicit API/CLI inspection boundaries.
type CustomerOperationsData struct {
	Workflows            []CustomerOperationWorkflow
	Milestones           []CustomerOperationMilestone
	MilestonesVisible    bool
	MilestonesError      string
	StaleOnly            bool
	NextMilestonesURL    string
	NextWorkflowStateURL string
	SubjectType          string
	SubjectID            string
	AppSlug              string
	ListURL              string
	Scope                string
	TenantID             string
	Name                 string
	State                string
	Limit                int
	Items                []CustomerOperationItem
	NextURL              string
	Detail               *CustomerOperationDetail
}

type CustomerOperationWorkflowRelation struct {
	api.OperationWorkflowRelatedInstance
	URL string
}

type CustomerOperationDependentWorkflow struct {
	api.OperationWorkflowDependentInstance
	URL string
}

type CustomerOperationDependencyTraceStep struct {
	api.OperationWorkflowDependency
	URL string
}

type CustomerOperationDependencyFinding struct {
	Title string
	api.OperationWorkflowDependencyFinding
	Steps []CustomerOperationDependencyTraceStep
}

type CustomerOperationTransitionReadiness struct {
	api.OperationWorkflowTransitionReadiness
	ReasonLabels []string
}

type CustomerOperationWorkflow struct {
	Readiness                       *api.OperationWorkflowReadinessOverview
	ReadinessItems                  []CustomerOperationTransitionReadiness
	DependencyTrace                 *api.OperationWorkflowDependencyTrace
	DependencyFindings              []CustomerOperationDependencyFinding
	DependencyImpact                *api.OperationWorkflowDependencyImpact
	DependentWorkflows              []CustomerOperationDependentWorkflow
	RelatedWorkflows                []CustomerOperationWorkflowRelation
	OutcomeCode, OutcomeDescription string
	Decision                        *api.OperationWorkflowDecision
	Name                            string
	Title                           string
	InstanceID                      string
	DeadlineAt                      string
	Overdue                         bool
	OverdueSeconds                  int64
	State                           string
	Terminal                        bool
	Stale                           bool
	StateOccurredAt                 string
	StateStaleAfter                 string
	StateUpdatedAt                  string
	StateRevision                   int64
	HistoryURL                      string
	Selected                        bool
	StateHistory                    []CustomerOperationWorkflowStateHistory
	Steps                           []CustomerOperationWorkflowStep
}

type CustomerOperationWorkflowStateHistory struct {
	DependenciesOnly                bool
	DependsOn                       []api.OperationWorkflowDependency
	OutcomeCode, OutcomeDescription string
	OutcomeOnly                     bool
	BlockerResolutions              []api.OperationWorkflowBlockerResolution
	DeadlineAt                      string
	DeadlineOnly                    bool
	BlockersOnly                    bool
	ID, OperationID, OperationURL   string
	FromState, State                string
	Revision                        int64
	OccurredAt, PublishedAt         string
}

type CustomerOperationWorkflowStep struct {
	Compensation    *api.OperationBusinessCompensation
	SourceEffectURL string
	Effect          *api.OperationBusinessEffect
	Invariant       *api.OperationBusinessInvariant
	Reconciliation  *api.OperationWorkflowReconciliation
	Decision        *api.OperationBusinessDecision
	Position        int
	Label           string
	MilestoneID     string
	OperationID     string
	OperationURL    string
	Payload         string
	OccurredAt      string
	PublishedAt     string
}

type CustomerOperationItem struct {
	Subject               *api.OperationSubject
	SubjectURL            string
	ID                    string
	Name                  string
	TenantID              string
	State                 string
	StateClass            string
	DetailURL             string
	Progress              *api.OperationProgress
	DeliveryState         string
	DeliveryAttempts      int
	CancellationRequested bool
	CreatedAt             string
	UpdatedAt             string
}

type CustomerOperationDetail struct {
	CustomerOperationItem
	Generation         int
	DefinitionRevision string
	DeploymentID       string
	DeploymentURL      string
	ReleaseID          string
	FailureCode        string
	ExpiresAt          string
	ResultAvailable    bool
	RecordURL          string
	Stages             []string
	DefinitionError    string
	DeliveryID         string
	NextDeliveryAt     string
	Executions         []CustomerOperationExecution
	ExecutionsError    string
	NextExecutionsURL  string
	Events             []CustomerOperationEvent
	EventsError        string
	EventsResync       bool
	NextEventsURL      string
	Artifacts          []CustomerOperationArtifact
}

type CustomerOperationExecution struct {
	Generation   int
	InvocationID string
	URL          string
	State        string
	Attempts     int
	CreatedAt    string
	CompletedAt  string
}

type CustomerOperationEvent struct {
	Sequence     int64
	Label        string
	CreatedAt    string
	InvocationID string
	ExecutionURL string
	Attempt      int
	Progress     *api.OperationProgress
}

type CustomerOperationArtifact struct {
	Name        string
	SizeBytes   int64
	ExpiresAt   string
	DownloadURL string
}

// Payload contains only explicitly declared, schema-validated public facts.
type CustomerOperationMilestone struct {
	Compensation                                                        *api.OperationBusinessCompensation
	SourceEffectURL                                                     string
	Effect                                                              *api.OperationBusinessEffect
	Invariant                                                           *api.OperationBusinessInvariant
	Reconciliation                                                      *api.OperationWorkflowReconciliation
	Decision                                                            *api.OperationBusinessDecision
	ID, Name, OperationID, OperationURL, Payload, OccurredAt, CreatedAt string
	WorkflowMapped                                                      bool
}
