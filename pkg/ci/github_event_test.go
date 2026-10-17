package ci

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func prSourcePayload() map[string]any {
	repo := map[string]any{"id": 123, "full_name": "gregale/ci-qualification"}
	return map[string]any{"action": "opened", "number": 9, "installation": map[string]any{"id": 41}, "repository": repo, "pull_request": map[string]any{
		"state": "open", "draft": false, "base": map[string]any{"ref": "main", "repo": repo}, "head": map[string]any{"sha": strings.Repeat("b", 40), "repo": repo}}}
}

func TestGitHubEventsRejectForksMissingIdentityAndUnsupportedTriggers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"fork", func(p map[string]any) {
			p["pull_request"].(map[string]any)["head"].(map[string]any)["repo"] = map[string]any{"id": 999, "full_name": "attacker/fork"}
		}},
		{"missing-head", func(p map[string]any) { p["pull_request"].(map[string]any)["head"].(map[string]any)["repo"] = nil }},
		{"missing-base", func(p map[string]any) { p["pull_request"].(map[string]any)["base"].(map[string]any)["repo"] = nil }},
		{"wrong-base-id", func(p map[string]any) {
			p["pull_request"].(map[string]any)["base"].(map[string]any)["repo"] = map[string]any{"id": 999, "full_name": "gregale/ci-qualification"}
		}},
		{"wrong-head-name", func(p map[string]any) {
			p["pull_request"].(map[string]any)["head"].(map[string]any)["repo"] = map[string]any{"id": 123, "full_name": "attacker/other"}
		}},
		{"closed", func(p map[string]any) { p["action"] = "closed" }},
		{"draft", func(p map[string]any) { p["pull_request"].(map[string]any)["draft"] = true }},
		{"edited", func(p map[string]any) { p["action"] = "edited" }},
		{"mutable-head", func(p map[string]any) { p["pull_request"].(map[string]any)["head"].(map[string]any)["sha"] = "main" }},
		{"zero-head", func(p map[string]any) {
			p["pull_request"].(map[string]any)["head"].(map[string]any)["sha"] = strings.Repeat("0", 40)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := prSourcePayload()
			tc.change(payload)
			body, _ := json.Marshal(payload)
			if _, err := DecodeGitHubEvent("pull_request", body); err == nil {
				t.Fatal("unsupported or untrusted PR was admitted")
			}
		})
	}
	payload := prSourcePayload()
	body, _ := json.Marshal(payload)
	event, err := DecodeGitHubEvent("pull_request", body)
	if err != nil || event.CommitSHA != strings.Repeat("b", 40) || event.PRNumber != 9 || event.Branch != "main" {
		t.Fatalf("PR source was not pinned: %+v %v", event, err)
	}
	first := event.Key()
	payload["action"] = "synchronize"
	body, _ = json.Marshal(payload)
	event, err = DecodeGitHubEvent("pull_request", body)
	if err != nil || first != event.Key() {
		t.Fatal("same PR head created different semantic identities")
	}
	payload["action"] = "edited"
	payload["changes"] = map[string]any{"base": map[string]any{"ref": map[string]any{"from": "release"}}}
	body, _ = json.Marshal(payload)
	if edited, err := DecodeGitHubEvent("pull_request", body); err != nil || edited.Key() != first {
		t.Fatal("retarget event lost the PR's semantic head/base identity")
	}
	for _, ref := range []string{"refs/tags/v1", "refs/pull/9/merge"} {
		body, _ := json.Marshal(map[string]any{"ref": ref, "after": strings.Repeat("a", 40)})
		if _, err := DecodeGitHubEvent("push", body); !errors.Is(err, ErrGitHubIgnored) {
			t.Fatal("non-branch ref admitted")
		}
	}
	for _, branch := range []string{"", "main*", "a..b", "../main", "a.lock", "a//b", "a\x01b", "a@{b", "refs/heads/.secret"} {
		if _, err := NormalizeGitHubBinding(GitHubBindingRequest{RevisionID: uuid.NewString(), InstallationID: 1, RepositoryID: 2, PushBranches: []string{branch}}); err == nil {
			t.Fatalf("unsafe branch filter accepted: %q", branch)
		}
	}
}

func TestGitHubFlagsFailClosed(t *testing.T) {
	account := uuid.NewString()
	for _, bad := range []string{"", "0", "true", "yes", " 1"} {
		cfg := ConfigFromEnv(func(key string) string {
			if key == AccountsEnv {
				return account
			}
			if key == InternalEnv {
				return "1"
			}
			return bad
		})
		if cfg.GitHubAllows(account) || len(cfg.GitHubAccounts()) != 0 || cfg.GitHubChecksEnabled {
			t.Fatalf("invalid GitHub gate enabled CI: %q", bad)
		}
	}
}
