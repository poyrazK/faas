package hosting

import (
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

var hostingGitHubID = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
var hostingGitHubRepository = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var hostingGitHubWorkflow = regexp.MustCompile(`^[A-Za-z0-9_.-]+\.ya?ml$`)

func ValidHostingOIDCTrust(r api.HostingOIDCTrustRules) bool {
	if !hostingGitHubID.MatchString(r.RepositoryOwnerID) || !hostingGitHubID.MatchString(r.RepositoryID) || len(r.Repository) > api.HostingOIDCRepositoryMaxBytes || !hostingGitHubRepository.MatchString(r.Repository) {
		return false
	}
	branch, ok := strings.CutPrefix(r.Ref, "refs/heads/")
	if !ok || branch == "" || len(r.Ref) > api.HostingOIDCRefMaxBytes || strings.ContainsAny(branch, " ~^:?*[\\\x00\r\n\t") || strings.Contains(branch, "..") || strings.Contains(branch, "@{") || strings.Contains(branch, "//") || strings.HasSuffix(branch, "/") {
		return false
	}
	for _, c := range branch {
		if c <= 32 || c == 127 {
			return false
		}
	}
	workflow, ok := strings.CutPrefix(r.WorkflowRef, r.Repository+"/.github/workflows/")
	workflow, suffix := strings.CutSuffix(workflow, "@"+r.Ref)
	if !ok || !suffix || len(r.WorkflowRef) > api.HostingOIDCWorkflowMaxBytes || !hostingGitHubWorkflow.MatchString(workflow) || len(r.Environment) > api.HostingOIDCEnvironmentMaxBytes || strings.TrimSpace(r.Environment) != r.Environment || strings.ContainsAny(r.Environment, "\x00\r\n") {
		return false
	}
	return r.EventName == "push" || r.EventName == "workflow_dispatch" || r.EventName == "schedule"
}
