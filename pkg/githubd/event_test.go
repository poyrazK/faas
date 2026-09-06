// event_test.go — tests for the GitHub push-webhook decoder. Pins
// the field names + empty-Before semantics the changed-files filter
// relies on.
package githubd

import (
	"strings"
	"testing"
)

func TestDecodePush_RequiresRequiredFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name:    "happy path: all fields populated",
			body:    `{"ref":"refs/heads/main","before":"abc123","after":"def456","repository":{"full_name":"octo/api"}}`,
			wantErr: false,
		},
		{
			name:    "missing ref",
			body:    `{"before":"abc123","after":"def456","repository":{"full_name":"octo/api"}}`,
			wantErr: true,
		},
		{
			name:    "missing after",
			body:    `{"ref":"refs/heads/main","before":"abc123","repository":{"full_name":"octo/api"}}`,
			wantErr: true,
		},
		{
			name:    "missing repository.full_name",
			body:    `{"ref":"refs/heads/main","before":"abc123","after":"def456","repository":{}}`,
			wantErr: true,
		},
		{
			name:    "empty body",
			body:    ``,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := DecodePush([]byte(tc.body))
			gotErr := err != nil
			if gotErr != tc.wantErr {
				t.Fatalf("DecodePush() err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

// TestDecodePush_BeforeIsExtracted pins the Before field wiring.
// Service.HandlePushRequest reads Before to form the
// compare/{base}...{head} URL; an empty Before (first push on a
// branch) is treated by the caller as the "fall back to full fan-out"
// signal — the decoder itself accepts it.
func TestDecodePush_BeforeIsExtracted(t *testing.T) {
	t.Parallel()
	body := []byte(`{"ref":"refs/heads/main","before":"abc123","after":"def456","repository":{"full_name":"octo/api"}}`)
	ev, err := DecodePush(body)
	if err != nil {
		t.Fatalf("DecodePush() err = %v", err)
	}
	if ev.Before != "abc123" {
		t.Errorf("Before = %q, want %q", ev.Before, "abc123")
	}
	if ev.After != "def456" {
		t.Errorf("After = %q, want %q", ev.After, "def456")
	}
	if ev.Ref != "refs/heads/main" {
		t.Errorf("Ref = %q, want %q", ev.Ref, "refs/heads/main")
	}
	if ev.Repository.FullName != "octo/api" {
		t.Errorf("Repository.FullName = %q, want %q", ev.Repository.FullName, "octo/api")
	}
}

// TestDecodePush_EmptyBeforeIsAccepted pins the first-push semantics:
// Before is the only required-ish field the decoder tolerates empty.
// Service.HandlePushRequest falls back to full fan-out when Before
// is empty; the decoder itself does NOT reject (the SHA may be
// 0000...0000 on a fresh branch, which GitHub still emits — but we
// don't want to misclassify an empty/missing field as a config bug
// when GitHub's payload may legitimately omit it).
func TestDecodePush_EmptyBeforeIsAccepted(t *testing.T) {
	t.Parallel()
	body := []byte(`{"ref":"refs/heads/main","after":"def456","repository":{"full_name":"octo/api"}}`)
	ev, err := DecodePush(body)
	if err != nil {
		t.Fatalf("DecodePush() err = %v, want nil (empty Before is tolerated)", err)
	}
	if ev.Before != "" {
		t.Errorf("Before = %q, want empty", ev.Before)
	}
}

func TestPushEvent_DeploySkipMarker(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		body   string
		marker string
	}{
		{name: "head commit", body: `{"ref":"refs/heads/main","after":"sha","repository":{"full_name":"octo/api"},"head_commit":{"message":"release [skip deploy]"}}`, marker: "[skip deploy]"},
		{name: "commit list case insensitive", body: `{"ref":"refs/heads/main","after":"sha","repository":{"full_name":"octo/api"},"commits":[{"message":"docs [DEPLOY SKIP]"}]}`, marker: "[deploy skip]"},
		{name: "ordinary commit", body: `{"ref":"refs/heads/main","after":"sha","repository":{"full_name":"octo/api"},"head_commit":{"message":"release"}}`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ev, err := DecodePush([]byte(tc.body))
			if err != nil {
				t.Fatalf("DecodePush: %v", err)
			}
			if got := ev.DeploySkipMarker(); got != tc.marker {
				t.Errorf("DeploySkipMarker() = %q, want %q", got, tc.marker)
			}
		})
	}
}

// validPRBody is a complete pull_request body the happy-path tests
// reuse. The head SHA is the standard 40-char hex form; the head
// repo's full_name matches the base repo's full_name (i.e. NOT a
// fork). Action is "opened".
const validPRBody = `{
  "action": "opened",
  "number": 42,
  "pull_request": {
    "state": "open",
    "head_sha": "0123456789abcdef0123456789abcdef01234567",
    "head_ref": "feat/foo",
    "head": {"ref": "feat/foo", "sha": "0123456789abcdef0123456789abcdef01234567", "repo": {"full_name": "octo/api"}}
  },
  "repository": {"full_name": "octo/api", "name": "api", "html_url": "https://github.com/octo/api"},
  "installation": {"id": 99},
  "sender": {"login": "octocat"}
}`

func TestDecodePullRequest_HappyPath(t *testing.T) {
	t.Parallel()
	ev, err := DecodePullRequest([]byte(validPRBody))
	if err != nil {
		t.Fatalf("DecodePullRequest() err = %v", err)
	}
	if ev.Action != PullRequestActionOpened {
		t.Errorf("Action = %q, want opened", ev.Action)
	}
	if ev.Number != 42 {
		t.Errorf("Number = %d, want 42", ev.Number)
	}
	if ev.PullRequest.HeadSHA != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("HeadSHA = %q, want 40-char hex", ev.PullRequest.HeadSHA)
	}
	if ev.PullRequest.State != "open" {
		t.Errorf("State = %q, want open", ev.PullRequest.State)
	}
	if ev.Repository.FullName != "octo/api" {
		t.Errorf("Repository.FullName = %q, want octo/api", ev.Repository.FullName)
	}
	if ev.Installation.ID != 99 {
		t.Errorf("Installation.ID = %d, want 99", ev.Installation.ID)
	}
	if ev.Sender.Login != "octocat" {
		t.Errorf("Sender.Login = %q, want octocat", ev.Sender.Login)
	}
	if ev.IsFork() {
		t.Errorf("IsFork() = true, want false (head and base are the same repo)")
	}
}

func TestDecodePullRequest_ActionsAccepted(t *testing.T) {
	t.Parallel()
	cases := []PullRequestAction{
		PullRequestActionOpened,
		PullRequestActionSynchronize,
		PullRequestActionReopened,
		PullRequestActionClosed,
	}
	for _, a := range cases {
		a := a
		t.Run(string(a), func(t *testing.T) {
			t.Parallel()
			body := strings.Replace(validPRBody, `"action": "opened"`, `"action": "`+string(a)+`"`, 1)
			if _, err := DecodePullRequest([]byte(body)); err != nil {
				t.Fatalf("DecodePullRequest(%s) err = %v", a, err)
			}
		})
	}
}

func TestDecodePullRequest_RejectsUnknownAction(t *testing.T) {
	t.Parallel()
	cases := []string{"assigned", "labeled", "review_requested", "edited", "ready_for_review"}
	for _, a := range cases {
		a := a
		t.Run(a, func(t *testing.T) {
			t.Parallel()
			body := strings.Replace(validPRBody, `"action": "opened"`, `"action": "`+a+`"`, 1)
			_, err := DecodePullRequest([]byte(body))
			if err == nil {
				t.Fatalf("DecodePullRequest(%s) err = nil, want rejection", a)
			}
			if !strings.Contains(err.Error(), "action not in") {
				t.Errorf("err = %v, want error mentioning 'action not in'", err)
			}
		})
	}
}

func TestDecodePullRequest_RejectsMissingFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		mutate   func(string) string
		wantFrag string
	}{
		{
			name:     "empty body",
			mutate:   func(b string) string { return "" },
			wantFrag: "empty",
		},
		{
			name:     "missing number (zero)",
			mutate:   func(b string) string { return strings.Replace(b, `"number": 42,`, `"number": 0,`, 1) },
			wantFrag: "number must be > 0",
		},
		{
			name: "short head SHA",
			mutate: func(b string) string {
				return strings.Replace(b, "0123456789abcdef0123456789abcdef01234567", "abc123", 1)
			},
			wantFrag: "40-char SHA",
		},
		{
			name:     "missing state",
			mutate:   func(b string) string { return strings.Replace(b, `"state": "open",`, `"state": "",`, 1) },
			wantFrag: "state must be",
		},
		{
			name: "missing base repo",
			mutate: func(b string) string {
				return strings.Replace(b, `"repository": {"full_name": "octo/api"`, `"repository": {"full_name": ""`, 1)
			},
			wantFrag: "missing repository.full_name",
		},
		{
			name: "missing installation.id",
			mutate: func(b string) string {
				return strings.Replace(b, `"installation": {"id": 99}`, `"installation": {"id": 0}`, 1)
			},
			wantFrag: "installation.id",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := tc.mutate(validPRBody)
			_, err := DecodePullRequest([]byte(body))
			if err == nil {
				t.Fatalf("DecodePullRequest() err = nil, want rejection containing %q", tc.wantFrag)
			}
			if !strings.Contains(err.Error(), tc.wantFrag) {
				t.Errorf("err = %v, want fragment %q", err, tc.wantFrag)
			}
		})
	}
}

func TestDecodePullRequest_ForkDetected(t *testing.T) {
	t.Parallel()
	// Same body, but head.repo.full_name differs from
	// repository.full_name — a fork PR.
	body := strings.Replace(validPRBody,
		`"head": {"ref": "feat/foo", "sha": "0123456789abcdef0123456789abcdef01234567", "repo": {"full_name": "octo/api"}}`,
		`"head": {"ref": "feat/foo", "sha": "0123456789abcdef0123456789abcdef01234567", "repo": {"full_name": "contributor/api"}}`,
		1,
	)
	ev, err := DecodePullRequest([]byte(body))
	if err != nil {
		t.Fatalf("DecodePullRequest() err = %v", err)
	}
	if !ev.IsFork() {
		t.Errorf("IsFork() = false, want true (head and base full_names differ)")
	}
	if ev.HeadRepoFullName() != "contributor/api" {
		t.Errorf("HeadRepoFullName() = %q, want contributor/api", ev.HeadRepoFullName())
	}
}

func TestDecodePullRequest_MissingHeadRepoIsNotFork(t *testing.T) {
	t.Parallel()
	// Drop the head.repo.full_name — a malformed payload. The
	// decoder must still parse the rest, and IsFork() must
	// return false (conservative: we cannot prove it's a fork).
	body := strings.Replace(validPRBody,
		`"head": {"ref": "feat/foo", "sha": "0123456789abcdef0123456789abcdef01234567", "repo": {"full_name": "octo/api"}}`,
		`"head": {"ref": "feat/foo", "sha": "0123456789abcdef0123456789abcdef01234567", "repo": {}}`,
		1,
	)
	ev, err := DecodePullRequest([]byte(body))
	if err != nil {
		t.Fatalf("DecodePullRequest() err = %v", err)
	}
	if ev.IsFork() {
		t.Errorf("IsFork() = true, want false (head.repo missing → conservative not-fork)")
	}
	if ev.HeadRepoFullName() != "" {
		t.Errorf("HeadRepoFullName() = %q, want empty", ev.HeadRepoFullName())
	}
}

func TestPullRequestAction_IsPreviewAction(t *testing.T) {
	t.Parallel()
	cases := []struct {
		action PullRequestAction
		want   bool
	}{
		{PullRequestActionOpened, true},
		{PullRequestActionSynchronize, true},
		{PullRequestActionReopened, true},
		{PullRequestActionClosed, true},
		{PullRequestAction("assigned"), false},
		{PullRequestAction(""), false},
		{PullRequestAction("OPENED"), false}, // case-sensitive — webhook bodies use lowercase
	}
	for _, tc := range cases {
		tc := tc
		t.Run(string(tc.action), func(t *testing.T) {
			t.Parallel()
			if got := tc.action.IsPreviewAction(); got != tc.want {
				t.Errorf("IsPreviewAction(%q) = %v, want %v", tc.action, got, tc.want)
			}
		})
	}
}
