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
	Slug, Name, Definition, Sample, Scenarios             string
	CSRFToken, ActionKey, BaseURL, Message, Error         string
	Version                                               int64
	RuntimeEnabled, Published, Enabled, FailurePaused     bool
	HealthError, RunsError, UnavailableReason, PolicyMode string
	Items                                                 []AutomationConsoleItem
	Starters                                              []AutomationStarterItem
	Validation                                            *api.ValidateAutomationResponse
	Simulation                                            *api.SimulateAutomationResponse
	CheckEvidence                                         *api.AutomationCheckEvidence
	Health                                                *api.AutomationHealthResponse
	Runs                                                  []AutomationConsoleRun
	Run                                                   *AutomationConsoleRun
	Steps                                                 []AutomationConsoleStep
}

type AutomationConsoleRun struct {
	ID, Name, Status, CurrentStep, CreatedAt, DeploymentID, URL string
}

type AutomationConsoleStep struct {
	Name, Status string
	Attempt      int
}
