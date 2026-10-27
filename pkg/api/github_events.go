package api

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// GitHub pull request events (ADR-20261027090010 §9). githubd hands each pull_request
// delivery to apid, which publishes a bounded copy as a platform event of the
// installation's account under the reserved gregale.github source. A workflow
// with an event trigger on that source starts from it, so an agent step can
// triage the pull request.
const (
	GitHubEventSource            = "gregale.github"
	GitHubPullRequestEventPrefix = "pull_request."
	gitHubEventURLMaxBytes       = 512
	gitHubEventNameMaxBytes      = 256
)

var ErrGitHubEventInvalid = errors.New("invalid GitHub event")

// gitHubPullRequestEventActions are the pull request actions that start
// automations; githubd forwards no others.
var gitHubPullRequestEventActions = map[string]bool{
	"opened": true, "reopened": true, "synchronize": true, "ready_for_review": true, "closed": true,
}

// GitHubPullRequestEventAction reports whether a pull_request action is
// published as a gregale.github event.
func GitHubPullRequestEventAction(action string) bool { return gitHubPullRequestEventActions[action] }

// GitHubPullRequestEvent is the data of a gregale.github event of type
// pull_request.<action>. It carries no pull request body or diff: an agent
// reads those through its checkout and GitHub target.
type GitHubPullRequestEvent struct {
	Action         string                 `json:"action"`
	InstallationID int64                  `json:"installation_id"`
	Repository     GitHubEventRepository  `json:"repository"`
	PullRequest    GitHubEventPullRequest `json:"pull_request"`
	Sender         string                 `json:"sender,omitempty"`
}

type GitHubEventRepository struct {
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch,omitempty"`
	Private       bool   `json:"private"`
}

type GitHubEventPullRequest struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Draft  bool   `json:"draft"`
	Merged bool   `json:"merged"`
	// Fork is true when the head branch lives in another repository.
	Fork   bool           `json:"fork"`
	URL    string         `json:"url,omitempty"`
	Author string         `json:"author,omitempty"`
	Labels []string       `json:"labels,omitempty"`
	Head   GitHubEventRef `json:"head"`
	Base   GitHubEventRef `json:"base"`
}

type GitHubEventRef struct {
	Ref        string `json:"ref"`
	SHA        string `json:"sha"`
	Repository string `json:"repository,omitempty"`
}

// Type is the event's type: pull_request.<action>.
func (e GitHubPullRequestEvent) Type() string { return GitHubPullRequestEventPrefix + e.Action }

// Validate bounds every field apid publishes.
func (e GitHubPullRequestEvent) Validate() error {
	invalid := func(why string) error { return fmt.Errorf("%w: %s", ErrGitHubEventInvalid, why) }
	pr := e.PullRequest
	switch {
	case !GitHubPullRequestEventAction(e.Action):
		return invalid("unsupported pull_request action")
	case e.InstallationID <= 0:
		return invalid("installation_id is required")
	case !validAgentRepository(e.Repository.FullName):
		return invalid("repository must be owner/name")
	case pr.Number <= 0:
		return invalid("pull_request.number is required")
	case pr.State != "open" && pr.State != "closed":
		return invalid("pull_request.state must be open or closed")
	case !agentCommit.MatchString(pr.Head.SHA) || !agentCommit.MatchString(pr.Base.SHA):
		return invalid("pull_request head and base need full commit SHAs")
	case pr.Head.Repository != "" && !validAgentRepository(pr.Head.Repository):
		return invalid("pull_request.head.repository must be owner/name")
	case pr.URL != "" && (len(pr.URL) > gitHubEventURLMaxBytes || !strings.HasPrefix(pr.URL, "https://")):
		return invalid("pull_request.url must be an https URL")
	case len(pr.Labels) > GitHubEventLabelsMax:
		return invalid(fmt.Sprintf("at most %d labels", GitHubEventLabelsMax))
	}
	if !gitHubEventText(pr.Title, GitHubEventTitleMaxBytes) {
		return invalid(fmt.Sprintf("pull_request.title must be UTF-8 text of at most %d bytes", GitHubEventTitleMaxBytes))
	}
	for _, label := range pr.Labels {
		if label == "" || !gitHubEventText(label, GitHubEventLabelMaxBytes) {
			return invalid(fmt.Sprintf("labels must be UTF-8 text of at most %d bytes", GitHubEventLabelMaxBytes))
		}
	}
	for _, name := range []string{e.Repository.DefaultBranch, pr.Head.Ref, pr.Base.Ref, pr.Author, e.Sender} {
		if !gitHubEventText(name, gitHubEventNameMaxBytes) {
			return invalid(fmt.Sprintf("names and refs must be UTF-8 text of at most %d bytes", gitHubEventNameMaxBytes))
		}
	}
	return nil
}

func gitHubEventText(s string, max int) bool {
	return len(s) <= max && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
