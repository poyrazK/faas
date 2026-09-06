// Webhook event decoders (slice 7 push; issue #272 PR-preview
// environments pull_request). Mirrors the GitHub webhook bodies —
// only the fields githubd cares about are decoded. The upstream
// GitHub schema is much richer; we keep the surface narrow so a
// schema change on GitHub's side is a local edit here.
package githubd

import (
	"encoding/json"
	"errors"
)

// PullRequestAction enumerates the GitHub `pull_request` webhook
// actions githubd reacts to. Anything not in this set is logged
// and ignored — GitHub emits ~10 actions and we only want the
// "PR was updated" trio plus "closed" for teardown.
type PullRequestAction string

const (
	PullRequestActionOpened      PullRequestAction = "opened"
	PullRequestActionSynchronize PullRequestAction = "synchronize"
	PullRequestActionReopened    PullRequestAction = "reopened"
	PullRequestActionClosed      PullRequestAction = "closed"
)

// IsPreviewAction reports whether the action is one of the four
// PR-preview events the decoder recognises. Used by
// handlePullRequest to short-circuit on unknown actions.
func (a PullRequestAction) IsPreviewAction() bool {
	switch a {
	case PullRequestActionOpened,
		PullRequestActionSynchronize,
		PullRequestActionReopened,
		PullRequestActionClosed:
		return true
	default:
		return false
	}
}

// PullRequestEvent is the subset of the GitHub pull_request webhook
// githubd parses to provision or tear down a preview environment
// (issue #272, ADR-094). The body is shared across all four
// preview actions; downstream code keys off `Action`.
//
// Decoded from the raw request body inside handlePullRequest.
// The caller verifies the HMAC signature BEFORE invoking the
// decoder — DecodePullRequest only surfaces parse errors.
type PullRequestEvent struct {
	Action       PullRequestAction   `json:"action"`       // opened/synchronize/reopened/closed
	Number       int                 `json:"number"`       // PR number; slug prefix `pr-{N}-`
	PullRequest  PullRequestPayload  `json:"pull_request"` // head SHA + ref + state + head repo
	Repository   PushRepository      `json:"repository"`   // base repo (for the binding lookup)
	Installation InstallationPayload `json:"installation"`
	Sender       SenderPayload       `json:"sender"`
}

// PullRequestPayload holds the fields githubd reads off the
// pull_request object. State is the lifecycle ("open"/"closed")
// and is what the closed→stale→torn_down state machine keys off.
//
// HeadSHA is the commit SHA at the tip of the PR branch; this is
// the value we send to the source-ref deploy path. HeadRef is the
// branch name (e.g. "feat/foo") — useful for the dashboard.
// Head.Repo.FullName is what the fork detector compares against
// Repository.FullName.
type PullRequestPayload struct {
	State   string             `json:"state"` // "open" or "closed"
	HeadSHA string             `json:"head_sha"`
	HeadRef string             `json:"head_ref"` // branch name
	Head    PullRequestHeadRef `json:"head"`
}

// PullRequestHeadRef carries the head side of a PR — used to
// detect forks (D3). GitHub emits a `head` object with both
// `ref` (branch name, duplicated for convenience) and `repo`
// (the head repo identity).
type PullRequestHeadRef struct {
	Ref  string              `json:"ref"`  // branch name (duplicated)
	SHA  string              `json:"sha"`  // commit SHA (duplicated)
	Repo PullRequestHeadRepo `json:"repo"` // head repo identity
}

// PullRequestHeadRepo is the head-side repo identity. Compared
// against PullRequestEvent.Repository.FullName (the base repo) by
// IsFork. Same-named forks in different orgs are correctly
// flagged because GitHub tags them with different full_names.
type PullRequestHeadRepo struct {
	FullName string `json:"full_name"`
}

// InstallationPayload is the GitHub App install identity —
// githubd reads installation.id to mint install tokens.
type InstallationPayload struct {
	ID int64 `json:"id"`
}

// SenderPayload is the GitHub login who triggered the event.
// Captured for the dashboard's audit log; never trusted for auth.
type SenderPayload struct {
	Login string `json:"login"`
}

// HeadRepoFullName returns the head repository's full_name. Used
// by IsFork. Returns "" when the JSON omits the field (which can
// happen if GitHub deletes the head repo after a long close).
func (ev *PullRequestEvent) HeadRepoFullName() string {
	return ev.PullRequest.Head.Repo.FullName
}

// IsFork reports whether the PR head repo is a fork of the base
// repo. Fork PRs are refused per ADR-094 D3 — the webhook handler
// short-circuits with a neutral Check Run and never provisions an
// app row.
func (ev *PullRequestEvent) IsFork() bool {
	head := ev.HeadRepoFullName()
	if head == "" {
		// Head repo missing — treat conservatively as not-fork
		// (we cannot prove it IS a fork, so don't refuse). This
		// means a malformed payload where head.repo is absent
		// gets the regular path; we'll surface the missing-field
		// error from DecodePullRequest if the SHA is also missing.
		return false
	}
	return head != ev.Repository.FullName
}

// IsForkOfMissingBase reports whether the head repo's full_name
// is non-empty AND the base repo's full_name is empty. That's
// the malformed-payload case the decoder can't catch from the
// fork comparison alone (it'd look like a fork of "").
func (ev *PullRequestEvent) IsForkOfMissingBase() bool {
	return ev.HeadRepoFullName() != "" && ev.Repository.FullName == ""
}

// PushEvent is the subset of the GitHub push webhook githubd parses
// to decide if the push lands on a bound app's branch.
//
// Decoded from the raw request body inside
// (Service).onPush. We deliberately skip the dozens of fields
// GitHub attaches (pusher, organization, sender) — they're audit
// signal only and end up in slog if requested.
type PushEvent struct {
	Ref          string              `json:"ref"`        // "refs/heads/main"
	Before       string              `json:"before"`     // commit SHA the branch was at before the push; empty for the first push on a branch (0000...0000)
	After        string              `json:"after"`      // commit SHA the head now points at
	Created      bool                `json:"created"`    // true when this push created the ref
	Deleted      bool                `json:"deleted"`    // true when GitHub deletes a branch or tag
	Forced       bool                `json:"forced"`     // true when the ref was force-updated
	Repository   PushRepository      `json:"repository"` // repo identity
	Installation InstallationPayload `json:"installation"`
	Pusher       PushPusher          `json:"pusher"` // optional audit
}

// PushRepository is the bits of `repository` the dispatch logic
// needs to look up the binding. FullName is the canonical
// "owner/name" handle used in the app bindings table.
type PushRepository struct {
	FullName string `json:"full_name"`
	Name     string `json:"name"`
	HTMLURL  string `json:"html_url"`
	// DefaultBranch is the binding key used for tag deployments. A tag
	// has no branch of its own, so githubd resolves it against the
	// repository's configured default production branch.
	DefaultBranch string `json:"default_branch"`
}

// PushPusher is the actor who triggered the push. Captured for the
// dashboard's audit log; never trusted for auth.
type PushPusher struct {
	Name string `json:"name"`
}

// DecodePush parses a raw GitHub push body into a PushEvent. The
// caller is responsible for verifying the signature BEFORE
// decoding; DecodePush only surfaces parse errors.
func DecodePush(body []byte) (PushEvent, error) {
	var ev PushEvent
	if len(body) == 0 {
		return ev, errors.New("githubd: empty push body")
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		return ev, err
	}
	if ev.Ref == "" || ev.After == "" || ev.Repository.FullName == "" {
		return ev, errors.New("githubd: push missing required fields (ref/after/repository.full_name)")
	}
	return ev, nil
}

// DecodePullRequest parses a raw GitHub pull_request body into a
// PullRequestEvent. The caller is responsible for verifying the
// signature BEFORE decoding; DecodePullRequest only surfaces parse
// errors.
//
// Validation:
//   - body must be non-empty
//   - action must be one of the four preview actions (D1)
//   - number must be > 0
//   - pull_request.head_sha must be a 40-char hex SHA
//   - pull_request.state must be "open" or "closed"
//   - repository.full_name must be non-empty (the base repo)
//
// The decoder does NOT reject fork PRs — that's a downstream
// policy decision in handlePullRequest (D3). Missing head.repo
// is tolerated (treated as "not a fork" by IsFork).
func DecodePullRequest(body []byte) (PullRequestEvent, error) {
	var ev PullRequestEvent
	if len(body) == 0 {
		return ev, errors.New("githubd: empty pull_request body")
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		return ev, err
	}
	if !ev.Action.IsPreviewAction() {
		return ev, errors.New("githubd: pull_request action not in {opened,synchronize,reopened,closed}: " + string(ev.Action))
	}
	if ev.Number <= 0 {
		return ev, errors.New("githubd: pull_request number must be > 0")
	}
	if len(ev.PullRequest.HeadSHA) != 40 {
		return ev, errors.New("githubd: pull_request.head_sha must be a 40-char SHA")
	}
	if ev.PullRequest.State != "open" && ev.PullRequest.State != "closed" {
		return ev, errors.New("githubd: pull_request.state must be open or closed: " + ev.PullRequest.State)
	}
	if ev.Repository.FullName == "" {
		return ev, errors.New("githubd: pull_request missing repository.full_name (base repo)")
	}
	if ev.Installation.ID == 0 {
		return ev, errors.New("githubd: pull_request missing installation.id")
	}
	return ev, nil
}
