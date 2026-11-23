// adr: 20261112223000

package main

// GuideTasks lists the guide's task names in order.
func GuideTasks() []string {
	tasks := make([]string, 0, len(guidePages))
	for _, page := range guidePages {
		tasks = append(tasks, page.Task)
	}
	return tasks
}

// GuideCommands returns every command line a guide page tells the reader to run.
func GuideCommands(task string) []string {
	page, _ := lookupGuidePage(task)
	var out []string
	for _, step := range page.Steps {
		if step.Command != "" {
			out = append(out, step.Command)
		}
	}
	return out
}

// RenderGuidePage renders one page as `gregale guide <task>` prints it.
func RenderGuidePage(task string) string {
	page, _ := lookupGuidePage(task)
	return renderGuidePage(page)
}

// RenderGuideIndex renders the task index.
func RenderGuideIndex() string { return renderGuideIndex() }

const GuidePageMaxBytes = guidePageMaxBytes

// ResetAgentHint lets a test print the agent pointer again: a new process
// with no marker of an earlier pointer.
func ResetAgentHint(t interface{ Cleanup(func()) }) {
	old := agentHintMarker
	agentHintPrinted = false
	agentHintMarker = func() string { return "" }
	t.Cleanup(func() { agentHintMarker = old })
}

// AgentHintMarkerName names the marker file for the given environment.
func AgentHintMarkerName(getenv func(string) string) string { return agentHintMarkerName(getenv) }

// SetAgentHintMarker points the across-process record at path for this test
// and starts a new process.
func SetAgentHintMarker(t interface{ Cleanup(func()) }, path string) {
	old := agentHintMarker
	agentHintMarker = func() string { return path }
	agentHintPrinted = false
	t.Cleanup(func() { agentHintMarker = old })
}

// NewAgentHintProcess forgets the in-process pointer, as a new gregale
// process would; the marker file stays.
func NewAgentHintProcess() { agentHintPrinted = false }
