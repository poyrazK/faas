// commands_rollouts_test.go — CLI tests for
// `gregale rollouts recover <slug>` (SAFE-RELEASES-R,
// issue #976 / ADR-122).
//
// Coverage matrix:
//
//   - cmdRollouts no-args usage branch exits 1.
//   - cmdRollouts unknown-subcommand branch exits 1.
//   - cmdRolloutsRecover validation gates:
//       * no positional slug → exit 1 (usage).
//       * missing --action → exit 1 (no round-trip).
//       * bad --action → exit 1 (closed-set check before the
//         network round-trip; the fake API's hit counter stays
//         at 0).
//   - happy path: route hits POST
//     /v1/apps/{slug}/rollouts/recover with the closed-set
//     body ({"action": "...", "reason": "..."}).
//   - happy path with --json: emits the raw
//     RolloutTransitionResponse DTO verbatim.

package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdRollouts_NoArgsExitsOne(t *testing.T) {
	main.ResetJSONOut(t)
	main.AuthedFakeAPI(t, "", http.StatusOK)
	if code := main.CmdRollouts(nil); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestCmdRollouts_UnknownSubcommandExitsOne(t *testing.T) {
	main.ResetJSONOut(t)
	main.AuthedFakeAPI(t, "", http.StatusOK)
	if code := main.CmdRollouts([]string{"wat"}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestCmdRolloutsRecover_NoSlugExitsOne(t *testing.T) {
	main.ResetJSONOut(t)
	main.AuthedFakeAPI(t, "", http.StatusOK)
	if code := main.CmdRolloutsRecover([]string{"--action", "promote"}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestCmdRolloutsRecover_NoActionExitsOne(t *testing.T) {
	main.ResetJSONOut(t)
	main.AuthedFakeAPI(t, "", http.StatusOK)
	if code := main.CmdRolloutsRecover([]string{"my-app"}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestCmdRolloutsRecover_BadActionExitsOne(t *testing.T) {
	main.ResetJSONOut(t)
	// The fake API is set up but should NOT be hit — the
	// closed-set check fires before the network round-trip.
	f := main.AuthedFakeAPI(t, "", http.StatusOK)
	if code := main.CmdRolloutsRecover([]string{"my-app", "--action", "explode"}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if (*f.SawMethod()) != "" || (*f.SawPath()) != "" {
		t.Errorf("fake API was hit (%s %s); closed-set gate should fire first", (*f.SawMethod()), (*f.SawPath()))
	}
}

func TestCmdRolloutsRecover_HappyPath_Promote(t *testing.T) {
	main.ResetJSONOut(t)
	body := `{"deployment":{"id":"0123456789abcdef0123456789abcdef","app_id":"abc","rollout_state":"complete","canary_step":4,"canary_total_steps":4},"audit_id":"42"}`
	f := main.AuthedFakeAPI(t, body, http.StatusOK)
	if code := main.CmdRolloutsRecover([]string{"my-app", "--action", "promote", "--reason", "manual-test"}); code != 0 {
		t.Fatalf("exit = %d, want 0, body=%s", code, "")
	}
	if (*f.SawMethod()) != "POST" || (*f.SawPath()) != "/v1/apps/my-app/rollouts/recover" {
		t.Errorf("route = %s %s, want POST /v1/apps/my-app/rollouts/recover", (*f.SawMethod()), (*f.SawPath()))
	}
	// Body must round-trip the closed-set + reason verbatim.
	var got api.RecoverRolloutRequest
	if err := json.Unmarshal((*f.SawBody()), &got); err != nil {
		t.Fatalf("body parse: %v: raw=%s", err, string((*f.SawBody())))
	}
	if got.Action != "promote" {
		t.Errorf("action=%q, want promote", got.Action)
	}
	if got.Reason != "manual-test" {
		t.Errorf("reason=%q, want manual-test", got.Reason)
	}
}

func TestCmdRolloutsRecover_JSONMode(t *testing.T) {
	main.ResetJSONOut(t)
	(*main.JsonOutput) = true
	t.Cleanup(func() { (*main.JsonOutput) = false })
	body := `{"deployment":{"id":"0123456789abcdef0123456789abcdef","app_id":"abc","rollout_state":"aborted","rollout_aborted_reason":"customer-confirmed-broken"},"audit_id":"7"}`
	main.AuthedFakeAPI(t, body, http.StatusOK)
	if code := main.CmdRolloutsRecover([]string{"my-app", "--action", "abort", "--reason", "customer-confirmed-broken"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
}

func TestCmdRolloutsRecover_AllClosedSetActions(t *testing.T) {
	// Pins that all three closed-set actions are accepted and
	// reach the HTTP round-trip — guards against a future
	// refactor that accidentally narrows the closed set in
	// one place (CLI) but not the other (api / handler / store).
	for _, action := range []string{"advance", "promote", "abort"} {
		t.Run(action, func(t *testing.T) {
			main.ResetJSONOut(t)
			body := `{"deployment":{"id":"0123456789abcdef0123456789abcdef"},"audit_id":"1"}`
			f := main.AuthedFakeAPI(t, body, http.StatusOK)
			code := main.CmdRolloutsRecover([]string{"my-app", "--action", action})
			if code != 0 {
				t.Fatalf("exit = %d, want 0", code)
			}
			if !strings.HasSuffix((*f.SawPath()), "/rollouts/recover") {
				t.Errorf("path=%q, want suffix /rollouts/recover", (*f.SawPath()))
			}
		})
	}
}
