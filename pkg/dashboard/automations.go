package dashboard

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/automationgate"
)

type AutomationConsoleItem struct {
	Name, URL, Source         string
	Version, PublishedVersion int64
	Enabled, FailurePaused    bool
}

type AutomationStarterItem struct{ Name, Description, Setup, URL string }

// AutomationConsoleData keeps real run payloads out of the authoring workspace.
type AutomationConsoleData struct {
	Slug, Name, Definition, Sample, Scenarios                string
	CSRFToken, ActionKey, BaseURL, Message, Error            string
	Version                                                  int64
	RuntimeEnabled, Published, Enabled, FailurePaused        bool
	HealthError, RunsError, UnavailableReason, PolicyMode    string
	Items                                                    []AutomationConsoleItem
	Starters                                                 []AutomationStarterItem
	Validation                                               *api.ValidateAutomationResponse
	Simulation                                               *api.SimulateAutomationResponse
	CheckEvidence                                            *api.AutomationCheckEvidence
	TestSuiteSaved, TestReportFresh                          bool
	TestReport                                               *api.AutomationTestReport
	LiveCases                                                []string
	LiveID, LiveCase, LiveError, LiveRunURL                  string
	LiveEditorChanged                                        bool
	LiveHistory                                              *api.ListAutomationLiveVerificationsResponse
	LiveSelected                                             *api.AutomationLiveVerification
	LiveSuiteID, LiveSuiteError, LiveSuiteStartID            string
	LiveSuiteAuto                                            bool
	LiveSuiteProgress                                        *AutomationLiveProgress
	LiveSuites                                               *api.ListAutomationLiveSuitesResponse
	LiveSuite                                                *api.AutomationLiveSuite
	LiveSuiteBaselineID, LiveSuiteComparisonError            string
	LiveSuiteComparison                                      *api.AutomationLiveSuiteComparison
	Baseline                                                 *AutomationBaselineForm
	BaselineCatalog                                          *api.ListAutomationLiveBaselinesResponse
	BaselineCatalogError                                     string
	LiveProgress                                             *AutomationLiveProgress
	TestEditorMode                                           string
	TestCaseIndex, TestAssertionIndex                        int
	TestLiveIndex                                            int
	TestEditor                                               *AutomationTestEditor
	Health                                                   *api.AutomationHealthResponse
	Runs                                                     []AutomationConsoleRun
	Run                                                      *AutomationConsoleRun
	Steps                                                    []AutomationConsoleStep
	Diagnostics                                              *api.WorkflowRunDiagnosticsResponse
	StateExplanation, DiagnosticsError, RecoveryHistoryError string
	RecoveryHistory                                          []AutomationConsoleResume
	RunControlsDisabled                                      bool
	SelectedStep, AttemptsError                              string
	Attempts                                                 []AutomationConsoleAttempt
	EditorMode                                               string
	Report                                                   *AutomationReportForm
	SetupChecks                                              []AutomationSetupCheck
	RuntimeObserved, PublicationObserved, PublishedSchedule  bool
	DraftSchedule, PublishedSchedulePreview                  *api.WorkflowSchedulePreviewResponse
	PublishedScheduleError                                   string
	ScheduleConfigured                                       bool
	Webhook                                                  *AutomationWebhookForm
	Ingress                                                  *AutomationWebhookSetup
}

type AutomationLiveProgress struct {
	URL                                string
	IntervalMS, TimeoutMS, MaxRequests int
}

type AutomationTestEditor struct {
	Live                                   *AutomationLiveEditor
	Cases, Assertions                      []AutomationTestChoice
	Targets                                []string
	Name, Simulation, Step, State          string
	ItemIndex, Output, AttemptCount, Error string
	AssertOutput, HasCase, HasAssertion    bool
	CanAddCase, CanAddAssertion            bool
}

type AutomationLiveEditor struct {
	Assertions                          []AutomationTestChoice
	Targets                             []string
	RunStatus, Step, State              string
	ItemIndex, Output, AttemptCount     string
	Enabled, HasAssertion, AssertOutput bool
	CanAddAssertion                     bool
	MaxAttempts, MaxItemIndex           int
}

type AutomationTestChoice struct {
	Index int
	Label string
}

type AutomationWebhookForm struct{ Source, EventType, Filter, LookupPath, EnrichPath string }

// AutomationInboundEndpoint omits routing tokens and signing-secret material.
type AutomationInboundEndpoint struct {
	ID, Name, Provider, DeliveryPath, URL string
	Enabled                               bool
}

type AutomationWebhookSetup struct {
	EndpointID, EventID, Pattern, Filter, ActionKey string
	Endpoints                                       []AutomationInboundEndpoint
	Endpoint                                        *AutomationInboundEndpoint
	Binding                                         *api.WebhookAutomationBindingResponse
	BindingVersion                                  int64
	BindingObserved, Submitted, ControlsDisabled    bool
	EndpointsError, BindingError, ReceiptError      string
	Receipt                                         *api.WebhookAutomationReceiptResponse
	RunURL                                          string
}

type AutomationReportForm struct {
	Schedule, Timezone, CollectPath, DeliverPath string
	Overlap, CatchUp, CatchUpWindow              string
}

type AutomationSetupCheck struct{ Name, Status, Detail string }

type AutomationConsoleRun struct {
	ID, Name, Status, CurrentStep, CreatedAt, DeploymentID, URL string
	ResumeCount                                                 int
	CanCancel                                                   bool
	ResumeKey, CancelKey                                        string
}

type AutomationConsoleStep struct {
	Name, Status, Kind, AttemptsURL string
	Attempt, RetryBase              int
	NextRetryAt, NextCheckAt        *string
}

// AutomationConsoleAttempt excludes errors and managed-operation effect records.
type AutomationConsoleAttempt struct {
	Number                    int
	Status, StartedAt         string
	HTTPStatus                *int
	FinishedAt, NextAttemptAt *string
}

// AutomationConsoleResume deliberately excludes historical errors and actors.
type AutomationConsoleResume struct {
	Number                    int
	PreviousStatus, CreatedAt string
	Steps                     []string
}

// AutomationBaselineForm contains only retained evidence and promotion intent.
type AutomationBaselineForm struct {
	Name, Error, Policy, PromotionID, Command string
	RetirementID                              string
	Revision                                  int64
	Head                                      *api.AutomationLiveBaseline
	History                                   *api.AutomationLiveBaselineHistory
	Request                                   *api.PromoteAutomationLiveBaselineRequest
	RetirementRequest                         *api.RetireAutomationLiveBaselineRequest
	Preview                                   *automationgate.Result
	Ready                                     bool
}
