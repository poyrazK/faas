package dashboard

import "github.com/onebox-faas/faas/pkg/api"

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
