package ci

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type gitHubRepoIdentity struct {
	ID       int64  `json:"id"`
	FullName string `json:"full_name"`
}

// DecodeGitHubEvent is called only after githubd verifies the App HMAC. PRs
// require complete, matching base/head repository identities; fork and missing
// identities fail closed. The selected SHA is the immutable head, never a ref.
func DecodeGitHubEvent(kind string, body []byte) (GitHubEvent, error) {
	if kind != "push" && kind != "pull_request" {
		return GitHubEvent{}, ErrGitHubIgnored
	}
	if len(body) == 0 || len(body) > api.CIGitHubWebhookMaxBytes {
		return GitHubEvent{}, fmt.Errorf("invalid CI GitHub payload size")
	}
	var payload struct {
		Ref          string             `json:"ref"`
		After        string             `json:"after"`
		Before       string             `json:"before"`
		Deleted      bool               `json:"deleted"`
		Action       string             `json:"action"`
		Number       int                `json:"number"`
		Repository   gitHubRepoIdentity `json:"repository"`
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
		Changes struct {
			Base struct {
				Ref struct {
					From *string `json:"from"`
				} `json:"ref"`
			} `json:"base"`
		} `json:"changes"`
		PullRequest struct {
			State string `json:"state"`
			Draft bool   `json:"draft"`
			Head  struct {
				SHA  string             `json:"sha"`
				Repo gitHubRepoIdentity `json:"repo"`
			} `json:"head"`
			Base struct {
				Ref  string             `json:"ref"`
				Repo gitHubRepoIdentity `json:"repo"`
			} `json:"base"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return GitHubEvent{}, fmt.Errorf("decode CI GitHub event: %w", err)
	}
	event := GitHubEvent{Kind: kind, InstallationID: payload.Installation.ID, RepositoryID: payload.Repository.ID, Repository: payload.Repository.FullName}
	if kind == "push" {
		if payload.Deleted || !strings.HasPrefix(payload.Ref, "refs/heads/") || payload.After == strings.Repeat("0", 40) {
			return GitHubEvent{}, ErrGitHubIgnored
		}
		event.Branch, event.CommitSHA = strings.TrimPrefix(payload.Ref, "refs/heads/"), payload.After
		if ValidCommitSHA(payload.Before) && payload.Before != strings.Repeat("0", 40) {
			event.BeforeSHA = payload.Before
		}
	} else {
		switch payload.Action {
		case "opened", "reopened", "synchronize", "ready_for_review":
		case "edited":
			if payload.Changes.Base.Ref.From == nil {
				return GitHubEvent{}, ErrGitHubIgnored
			}
		default:
			return GitHubEvent{}, ErrGitHubIgnored
		}
		pr := payload.PullRequest
		if pr.State != "open" || pr.Draft || pr.Head.Repo.ID != event.RepositoryID || pr.Base.Repo.ID != event.RepositoryID ||
			!strings.EqualFold(pr.Head.Repo.FullName, event.Repository) || !strings.EqualFold(pr.Base.Repo.FullName, event.Repository) {
			return GitHubEvent{}, ErrGitHubIgnored
		}
		event.Branch, event.CommitSHA, event.PRNumber = pr.Base.Ref, pr.Head.SHA, payload.Number
	}
	if !event.valid() {
		return GitHubEvent{}, fmt.Errorf("invalid CI GitHub source identity")
	}
	return event, nil
}
