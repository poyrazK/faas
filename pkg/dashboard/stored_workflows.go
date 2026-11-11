package dashboard

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storedworkflowgate"
)

type StoredWorkflowConsoleItem struct {
	Name, URL, Source         string
	Version, PublishedVersion int64
	Enabled, FailurePaused    bool
}

type StoredWorkflowStarterItem struct{ Name, Description, Setup, URL string }

// StoredWorkflowConsoleData keeps real run payloads out of the authoring workspace.
type StoredWorkflowConsoleData struct {
	Slug, Name, Definition, Sample, Scenarios                string
	CSRFToken, ActionKey, BaseURL, Message, Error            string
	Version                                                  int64
	RuntimeEnabled, Published, Enabled, FailurePaused        bool
	HealthError, RunsError, UnavailableReason, PolicyMode    string
	Items                                                    []StoredWorkflowConsoleItem
	Starters                                                 []StoredWorkflowStarterItem
	Validation                                               *api.ValidateStoredWorkflowResponse
	Simulation                                               *api.SimulateStoredWorkflowResponse
	CheckEvidence                                            *api.StoredWorkflowCheckEvidence
	TestSuiteSaved, TestReportFresh                          bool
	TestReport                                               *api.StoredWorkflowTestReport
	LiveCases                                                []string
	LiveID, LiveCase, LiveError, LiveRunURL                  string
	LiveEditorChanged                                        bool
	LiveHistory                                              *api.ListStoredWorkflowLiveVerificationsResponse
	LiveSelected                                             *api.StoredWorkflowLiveVerification
	LiveSuiteID, LiveSuiteError, LiveSuiteStartID            string
	LiveSuiteAuto                                            bool
	LiveSuiteProgress                                        *StoredWorkflowLiveProgress
	LiveSuites                                               *api.ListStoredWorkflowLiveSuitesResponse
	LiveSuite                                                *api.StoredWorkflowLiveSuite
	LiveSuiteBaselineID, LiveSuiteComparisonError            string
	LiveSuiteComparison                                      *api.StoredWorkflowLiveSuiteComparison
	Baseline                                                 *StoredWorkflowBaselineForm
	BaselineCatalog                                          *api.ListStoredWorkflowLiveBaselinesResponse
	BaselineCatalogError                                     string
	LiveProgress                                             *StoredWorkflowLiveProgress
	TestEditorMode                                           string
	TestCaseIndex, TestAssertionIndex                        int
	TestLiveIndex                                            int
	TestEditor                                               *StoredWorkflowTestEditor
	Health                                                   *api.StoredWorkflowHealthResponse
	Runs                                                     []StoredWorkflowConsoleRun
	Run                                                      *StoredWorkflowConsoleRun
	Steps                                                    []StoredWorkflowConsoleStep
	Diagnostics                                              *api.WorkflowRunDiagnosticsResponse
	StateExplanation, DiagnosticsError, RecoveryHistoryError string
	RecoveryHistory                                          []StoredWorkflowConsoleResume
	RunControlsDisabled                                      bool
	SelectedStep, AttemptsError                              string
	Attempts                                                 []StoredWorkflowConsoleAttempt
	EditorMode                                               string
	Report                                                   *StoredWorkflowReportForm
	SetupChecks                                              []StoredWorkflowSetupCheck
	RuntimeObserved, PublicationObserved, PublishedSchedule  bool
	DraftSchedule, PublishedSchedulePreview                  *api.WorkflowSchedulePreviewResponse
	PublishedScheduleError                                   string
	ScheduleConfigured                                       bool
	Webhook                                                  *StoredWorkflowWebhookForm
	Ingress                                                  *StoredWorkflowWebhookSetup
}

type StoredWorkflowLiveProgress struct {
	URL                                string
	IntervalMS, TimeoutMS, MaxRequests int
}

type StoredWorkflowTestEditor struct {
	Live                                   *StoredWorkflowLiveEditor
	Cases, Assertions                      []StoredWorkflowTestChoice
	Targets                                []string
	Name, Simulation, Step, State          string
	ItemIndex, Output, AttemptCount, Error string
	AssertOutput, HasCase, HasAssertion    bool
	CanAddCase, CanAddAssertion            bool
}

type StoredWorkflowLiveEditor struct {
	Assertions                          []StoredWorkflowTestChoice
	Targets                             []string
	RunStatus, Step, State              string
	ItemIndex, Output, AttemptCount     string
	Enabled, HasAssertion, AssertOutput bool
	CanAddAssertion                     bool
	MaxAttempts, MaxItemIndex           int
}

type StoredWorkflowTestChoice struct {
	Index int
	Label string
}

type StoredWorkflowWebhookForm struct{ Source, EventType, Filter, LookupPath, EnrichPath string }

// StoredWorkflowInboundEndpoint omits routing tokens and signing-secret material.
type StoredWorkflowInboundEndpoint struct {
	ID, Name, Provider, DeliveryPath, URL string
	Enabled                               bool
}

type StoredWorkflowWebhookSetup struct {
	EndpointID, EventID, Pattern, Filter, ActionKey string
	Endpoints                                       []StoredWorkflowInboundEndpoint
	Endpoint                                        *StoredWorkflowInboundEndpoint
	Binding                                         *api.WebhookWorkflowBindingResponse
	BindingVersion                                  int64
	BindingObserved, Submitted, ControlsDisabled    bool
	EndpointsError, BindingError, ReceiptError      string
	Receipt                                         *api.WebhookWorkflowReceiptResponse
	RunURL                                          string
}

type StoredWorkflowReportForm struct {
	Schedule, Timezone, CollectPath, DeliverPath string
	Overlap, CatchUp, CatchUpWindow              string
}

type StoredWorkflowSetupCheck struct{ Name, Status, Detail string }

type StoredWorkflowConsoleRun struct {
	ID, Name, Status, CurrentStep, CreatedAt, DeploymentID, URL string
	ResumeCount                                                 int
	CanCancel                                                   bool
	ResumeKey, CancelKey                                        string
}

type StoredWorkflowConsoleStep struct {
	Name, Status, Kind, AttemptsURL string
	Attempt, RetryBase              int
	NextRetryAt, NextCheckAt        *string
}

// StoredWorkflowConsoleAttempt excludes errors and managed-operation effect records.
type StoredWorkflowConsoleAttempt struct {
	Number                    int
	Status, StartedAt         string
	HTTPStatus                *int
	FinishedAt, NextAttemptAt *string
}

// StoredWorkflowConsoleResume deliberately excludes historical errors and actors.
type StoredWorkflowConsoleResume struct {
	Number                    int
	PreviousStatus, CreatedAt string
	Steps                     []string
}

// StoredWorkflowBaselineForm contains only retained evidence and promotion intent.
type StoredWorkflowBaselineForm struct {
	Name, Error, Policy, PromotionID, Command string
	RetirementID                              string
	Revision                                  int64
	Head                                      *api.StoredWorkflowLiveBaseline
	History                                   *api.StoredWorkflowLiveBaselineHistory
	Request                                   *api.PromoteStoredWorkflowLiveBaselineRequest
	RetirementRequest                         *api.RetireStoredWorkflowLiveBaselineRequest
	Preview                                   *storedworkflowgate.Result
	Ready                                     bool
}
