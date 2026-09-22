//go:build !no_pg

package state_test

// Encoding contract for free text that crosses a serialization boundary.
//
// Two distinct defects share one root cause: nothing normalizes customer or
// error text before it becomes a Postgres value.
//
//  1. fmt.Sprintf with the %q verb is used to hand-build JSON at
//     pgstore.go:7712/:7740/:7793 (mirrored in memstore.go:6467/:6501/:6539).
//     %q is strconv.Quote, NOT a JSON encoder — it emits \xNN escapes for
//     control bytes and for invalid UTF-8, and JSON has no \x escape. The
//     result cannot be stored in events.data (jsonb).
//
//  2. Free text is truncated with a BYTE slice before it is written to a
//     Postgres text column (cmd/apid/handlers_rollouts.go:97,
//     pgstore_prewarm.go:131, pgstore_operator_intent.go:205, and ~12 more).
//     Cutting a multi-byte rune in half yields invalid UTF-8, which Postgres
//     rejects on any UTF8 database.
//
// Both failures land on recovery and error-reporting paths, so they destroy
// the operation that was already trying to report a problem. These tests pin
// the behaviour the platform should have; they fail on the current tree.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/state"
)

// quoteVerbAuditData reproduces, verbatim, the audit-payload construction in
// PgStore.RecoverRollout (pgstore.go:7793 and its two siblings).
func quoteVerbAuditData(action, reason string) string {
	return fmt.Sprintf(`{"action":%q,"reason":%q}`, action, reason)
}

// truncateLikeHandler reproduces cmd/apid/handlers_rollouts.go:97 verbatim.
// The comment there says "1024 chars"; the code counts bytes.
func truncateLikeHandler(reason string) string {
	if len(reason) > 1024 {
		reason = reason[:1024]
	}
	return reason
}

// controlCharReason is what a client gets by POSTing the legal JSON body
// {"action":"abort","reason":"rollback \u0001 bad deploy"} to
// POST /v1/apps/{slug}/rollout/recover. encoding/json decodes the \u0001
// escape to a raw 0x01 byte, and every validation gate in the handler passes
// it through untouched. Decoded here rather than written as a Go literal so
// the test documents the actual customer-reachable route.
func controlCharReason(t *testing.T) string {
	t.Helper()
	var req struct{ Reason string }
	body := []byte(`{"action":"abort","reason":"rollback \u0001 bad deploy"}`)
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("fixture body must be legal JSON (it is what a client may send): %v", err)
	}
	return req.Reason
}

// midRuneTruncatedReason is an ordinary non-ASCII reason longer than the
// handler's 1024-BYTE cap, cut so the final rune is split. No control
// characters are involved: any operator writing a long reason in German,
// Turkish, Japanese, or with an emoji can produce this.
func midRuneTruncatedReason() string {
	return truncateLikeHandler(strings.Repeat("a", 1023) + "ü") // 'ü' = 0xC3 0xBC
}

// TestAuditPayloadEncoding_QuoteVerbVsEncodingJSON pins WHY rolloutAuditData
// exists, against a real server rather than against the JSON spec.
//
// Each case is inserted into events.data twice: once encoded the way the code
// used to do it (fmt with the %q verb) and once the way it does it now
// (encoding/json over a struct, after safetext normalization). The %q form
// must be rejected and the current form must be accepted. If someone reverts
// the call sites to fmt, the second half of this test starts failing.
func TestAuditPayloadEncoding_QuoteVerbVsEncodingJSON(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)

	insert := func(t *testing.T, payload string) error {
		t.Helper()
		_, err := pool.Exec(ctx,
			`insert into events (actor, kind, data) values ($1, $2, $3::jsonb)`,
			"operator:cli:recover_rollout", "deploy.rolled_back", payload)
		return err
	}

	cases := []struct {
		name   string
		reason string
	}{
		{"control character from a legal JSON request body", controlCharReason(t)},
		{"non-ASCII reason cut mid-rune by a byte-slice cap", midRuneTruncatedReason()},
		{"NUL byte", "upstream said\x00nothing"},
		{"ANSI-coloured build output", "\x1b[31mFAILED\x1b[0m"},
		{"plain ASCII", "routine rollback"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The current encoder must always produce an insertable payload.
			if err := insert(t, string(state.RolloutAuditDataForTest("abort", tc.reason))); err != nil {
				t.Fatalf("the current encoder produced a payload Postgres rejected: %v\n"+
					"  reason valid UTF-8: %v", err, utf8.ValidString(tc.reason))
			}

			// The abandoned fmt form must still be rejected for the inputs
			// that motivated the change — otherwise this test has stopped
			// standing for anything.
			if utf8.ValidString(tc.reason) && !strings.ContainsAny(tc.reason, "\x00\x01\x1b") {
				return // benign input; %q and json agree here
			}
			if err := insert(t, quoteVerbAuditData("abort", tc.reason)); err == nil {
				t.Fatalf("the %%q form was accepted for %q — the defect this test "+
					"pins is no longer reproducible, so the test needs rewriting "+
					"rather than silently passing", tc.reason)
			}
		})
	}
}

// TestPgStore_RecoverRollout_SurvivesUnicodeReason drives the customer-reachable
// path end to end: seed a canary rollout, then abort it with a reason the API
// accepts. The rollout must actually abort.
//
// Note there is no other PgStore test for RecoverRollout anywhere in the tree —
// only MemStore ones (memstore_canary_rollout_test.go). MemStore wraps the same
// broken string in json.RawMessage, which never validates, so the MemStore
// tests pass and the real SQL path is unexercised. That is the test seam.
func TestPgStore_RecoverRollout_SurvivesUnicodeReason(t *testing.T) {
	cases := []struct {
		name   string
		slug   string
		reason func(*testing.T) string
	}{
		{"control character", "ctl", controlCharReason},
		{"non-ASCII cut mid-rune", "midrune", func(*testing.T) string { return midRuneTruncatedReason() }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason := tc.reason(t)
			s, pool, ctx := pgStoreWithPool(t)
			_, appID, depID := seedLiveDeploy(t, s, ctx, "rollout-"+tc.slug, "rollout"+tc.slug)

			// Put the live deployment into a mid-ladder canary rollout so the
			// "abort" branch is reachable.
			if _, err := pool.Exec(ctx, `update deployments set
					rollout_state = 'rolling_out',
					canary_preset = 'custom',
					canary_stages = '[10,25,100]'::jsonb,
					canary_step = 1,
					canary_total_steps = 3,
					canary_step_started_at = $2,
					traffic_percent = 25,
					rollout_started_at = $2
				where id = $1`, depID, time.Now().UTC().Add(-time.Hour)); err != nil {
				t.Fatalf("seed canary rollout: %v", err)
			}

			if _, _, err := s.RecoverRollout(ctx, appID, "abort", reason); err != nil {
				t.Fatalf("RecoverRollout(abort) failed on a reason the API accepts.\n"+
					"  reason is valid UTF-8: %v\n"+
					"  error: %v\n"+
					"The audit INSERT rides the same transaction as the deployment stamp, so\n"+
					"the abort is rolled back and the rollout stays stuck. This is the recovery\n"+
					"endpoint, so it fails exactly when it is needed.",
					utf8.ValidString(reason), err)
			}

			var rolloutState string
			if err := pool.QueryRow(ctx,
				`select rollout_state from deployments where id = $1`, depID,
			).Scan(&rolloutState); err != nil {
				t.Fatalf("read back rollout_state: %v", err)
			}
			if rolloutState == "rolling_out" {
				t.Fatalf("rollout_state is still %q — the abort was rolled back", rolloutState)
			}
		})
	}
}

// TestPgStore_FailPrewarmIntent_SurvivesInvalidUTF8Cause covers the second
// defect class on a path where the message is an err.Error() string rather
// than customer input: registry responses, guest output, and upstream API
// bodies all reach FailPrewarmIntent's `cause` and are byte-truncated at
// pgstore_prewarm.go:131 before the write.
//
// The failure is self-reinforcing: FailPrewarmIntent exists to move the row
// OUT of 'running' into 'failed'. If the cause is corrupt the UPDATE errors,
// the transition never happens, and a clean failure becomes a stuck intent.
func TestPgStore_FailPrewarmIntent_SurvivesInvalidUTF8Cause(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "prewarm-utf8", "prewarmutf8")
	now := time.Now().UTC()

	intent, err := s.CreatePrewarmIntent(ctx, appID, accountID, 1,
		now.Add(time.Minute), now.Add(5*time.Minute), state.PrewarmTriggerCalendar)
	if err != nil {
		t.Fatalf("CreatePrewarmIntent: %v", err)
	}
	if _, claimed, err := s.ClaimPrewarmIntent(ctx, intent.ID, now); err != nil || !claimed {
		t.Fatalf("ClaimPrewarmIntent: claimed=%v err=%v", claimed, err)
	}

	// A realistic upstream error whose 2048th byte falls INSIDE a multi-byte
	// rune, which is what the cut at pgstore_prewarm.go:131 splits. The prefix
	// is padded to exactly 2047 bytes so the Turkish text straddles the cap.
	prefix := strings.Repeat("registry returned 503; ", 89) // 2047 bytes
	cause := prefix + "üst akış zaman aşımına uğradı"       // 'ü' = 0xC3 0xBC straddles byte 2048
	if len(prefix) != 2047 {
		t.Fatalf("fixture prefix is %d bytes, want exactly 2047 so the cap lands mid-rune", len(prefix))
	}
	if utf8.ValidString(cause[:2048]) {
		t.Fatalf("fixture does not straddle the 2048-byte cap; truncation branch not exercised")
	}

	if err := s.FailPrewarmIntent(ctx, intent.ID, now, cause); err != nil {
		t.Fatalf("FailPrewarmIntent rejected a realistic upstream error message: %v\n"+
			"The intent stays in 'running' and is never reaped as failed.", err)
	}

	got, err := s.PrewarmIntentByID(ctx, intent.ID)
	if err != nil {
		t.Fatalf("PrewarmIntentByID: %v", err)
	}
	if got.Status != state.PrewarmStatusFailed {
		t.Fatalf("intent status = %q, want %q — the failure transition was lost",
			got.Status, state.PrewarmStatusFailed)
	}
	if !utf8.ValidString(got.LastError) {
		t.Fatalf("stored last_error is not valid UTF-8: %.80q", got.LastError)
	}
}
