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

// ResetAgentHint lets a test print the agent pointer again.
func ResetAgentHint() { agentHintPrinted = false }
