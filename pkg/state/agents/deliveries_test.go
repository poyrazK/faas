// adr: 20261027090010, 20261027090014
package agents_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/agents"
)

const installation = 42

var pr7 = &agents.GitHubTarget{Repository: "acme/app", Number: 7}

// deliveryFixture has a "reviewer" agent that comments on GitHub, the
// account's installation, and a receiver subscribed to finished runs.
func deliveryFixture(t *testing.T) (*pgxpool.Pool, *agents.Store) {
	t.Helper()
	pool, s := agentdFixture(t)
	ctx := context.Background()
	reviewer := spec()
	reviewer.Deliveries = []agents.Delivery{{Type: api.AgentDeliveryGitHubComment}}
	if _, _, err := s.CreateAgent(ctx, account, "reviewer", "", reviewer, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO github_installations (account_id, installation_id) VALUES ($1, $2)`, account, installation); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO app_webhooks (id, account_id, scope, enabled, event_filter)
		VALUES ($1, $2, 'account', true, ARRAY['agent_run.finished'])`, uuid.NewString(), account); err != nil {
		t.Fatal(err)
	}
	return pool, s
}

// succeed admits a reviewer run for target and drives it to a succeeded
// finish with result.
func succeed(t *testing.T, s *agents.Store, target *agents.GitHubTarget, result string, off bool) agents.Run {
	t.Helper()
	ctx := context.Background()
	run := admit(t, s, agents.Admission{Agent: "reviewer", GitHub: target})
	token := "tok-" + run.ID
	claim, found, err := s.ClaimRun(ctx, "agentd-1", token, ttl, maxLive(10))
	if err != nil || !found || claim.RunID != run.ID {
		t.Fatalf("claim %+v %v %v", claim, found, err)
	}
	if err := s.MarkRunning(ctx, run.ID, token, "agents-1", "agent-x", 1000); err != nil {
		t.Fatal(err)
	}
	exit := 0
	if err := s.FinishRun(ctx, account, run.ID, token, agents.Finish{State: agents.StateSucceeded, ExitCode: &exit,
		Result: json.RawMessage(result), DeliveriesOff: off}); err != nil {
		t.Fatal(err)
	}
	return run
}

func runState(t *testing.T, s *agents.Store, id string) agents.Run {
	t.Helper()
	run, err := s.Run(context.Background(), account, id)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func announced(t *testing.T, pool *pgxpool.Pool, runID string) []api.AgentRunFinishedWebhookPayload {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT payload FROM app_webhook_deliveries WHERE source_event_id = $1`, runID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []api.AgentRunFinishedWebhookPayload
	for rows.Next() {
		var body []byte
		var p api.AgentRunFinishedWebhookPayload
		if err := rows.Scan(&body); err != nil || json.Unmarshal(body, &p) != nil {
			t.Fatalf("payload %s %v", body, err)
		}
		out = append(out, p)
	}
	return out
}

func claimOne(t *testing.T, s *agents.Store) agents.DeliveryClaim {
	t.Helper()
	claims, _, err := s.ClaimDeliveries(context.Background(), uuid.NewString(), time.Minute, 10)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims %+v %v", claims, err)
	}
	return claims[0]
}

// makeDue lets every waiting delivery be claimed now.
func makeDue(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE agent_deliveries SET next_attempt_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
}

func TestDeliveringRunEndsWithItsDelivery(t *testing.T) {
	pool, s := deliveryFixture(t)
	ctx := context.Background()
	run := succeed(t, s, pr7, `{"comment":"Looks good."}`, false)
	if got := runState(t, s, run.ID); got.State != agents.StateDelivering || got.FinishedAt != nil || got.GitHub == nil ||
		got.GitHub.InstallationID != installation {
		t.Fatalf("after the harness: %+v", got)
	}
	if got := announced(t, pool, run.ID); len(got) != 0 {
		t.Fatalf("announced before delivering: %+v", got)
	}
	c := claimOne(t, s)
	if c.RunID != run.ID || c.Destination != api.AgentDeliveryGitHubComment || c.Body != "Looks good." || c.Repository != "acme/app" ||
		c.Number != 7 || c.InstallationID != installation || !strings.HasPrefix(c.Key, "agent-") || c.Attempt != 1 {
		t.Fatalf("claim %+v", c)
	}
	if again, _, err := s.ClaimDeliveries(ctx, uuid.NewString(), time.Minute, 10); err != nil || len(again) != 0 {
		t.Fatalf("a sending delivery was claimed again: %+v %v", again, err)
	}
	wrong := c
	wrong.LeaseToken = "someone-else"
	if _, err := s.EndDeliveryAttempt(ctx, wrong, agents.DeliveryResult{Outcome: agents.DeliveryDelivered}); !errors.Is(err, agents.ErrLeaseLost) {
		t.Fatalf("a foreign lease recorded the attempt: %v", err)
	}
	url := "https://github.com/acme/app/pull/7#issuecomment-99"
	if _, err := s.EndDeliveryAttempt(ctx, c, agents.DeliveryResult{Outcome: agents.DeliveryDelivered, ExternalID: "99", URL: url}); err != nil {
		t.Fatal(err)
	}
	if got := runState(t, s, run.ID); got.State != agents.StateSucceeded || got.FinishedAt == nil || got.OutcomeCode != "" {
		t.Fatalf("after delivering: %+v", got)
	}
	got := announced(t, pool, run.ID)
	if len(got) != 1 || got[0].State != agents.StateSucceeded || len(got[0].Deliveries) != 1 ||
		got[0].Deliveries[0].URL != url || got[0].Deliveries[0].State != api.AgentDeliveryDelivered || len(got[0].Result) == 0 {
		t.Fatalf("announcement %+v", got)
	}
	out, err := s.RunOutputs(ctx, account, run.ID)
	if err != nil || len(out.Deliveries) != 1 || out.Deliveries[0].Attempts != 1 {
		t.Fatalf("outputs %+v %v", out, err)
	}
}

func TestDeliveryAdmission(t *testing.T) {
	pool, s := deliveryFixture(t)
	ctx := context.Background()
	if _, _, err := s.AdmitRun(ctx, agents.Admission{AccountID: account, Agent: "reviewer", Trigger: "api", Limits: roomy}); !errors.Is(err, agents.ErrDeliveryTargetMissing) {
		t.Fatalf("a run without a target: %v", err)
	}
	named := &agents.GitHubTarget{Repository: "acme/app", Number: 7, InstallationID: 7}
	if _, _, err := s.AdmitRun(ctx, agents.Admission{AccountID: account, Agent: "reviewer", Trigger: "api", GitHub: named, Limits: roomy}); !errors.Is(err, agents.ErrGitHubUnavailable) {
		t.Fatalf("another account's installation: %v", err)
	}
	// A run of an agent without deliveries may still name a target; its
	// installation is fixed all the same.
	run := admit(t, s, agents.Admission{GitHub: pr7})
	if run.GitHub == nil || run.GitHub.InstallationID != installation {
		t.Fatalf("target %+v", run.GitHub)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM github_installations`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AdmitRun(ctx, agents.Admission{AccountID: account, Agent: "reviewer", Trigger: "api", GitHub: pr7, Limits: roomy}); !errors.Is(err, agents.ErrGitHubUnavailable) {
		t.Fatalf("no installation: %v", err)
	}
}

// A delivery that cannot start fails at once and fails its run.
func TestDeliveryThatCannotStart(t *testing.T) {
	pool, s := deliveryFixture(t)
	for name, tc := range map[string]struct {
		result, code string
		off          bool
	}{
		"no comment":       {`{"verdict":"ok"}`, "result_field_missing", false},
		"empty comment":    {`{"comment":"  "}`, "result_field_missing", false},
		"null comment":     {`{"comment":null}`, "result_field_missing", false},
		"deliveries off":   {`{"comment":"hi"}`, "deliveries_unavailable", true},
		"comment not text": {`{"comment":7}`, "result_field_missing", false},
	} {
		run := succeed(t, s, pr7, tc.result, tc.off)
		got := runState(t, s, run.ID)
		out, err := s.RunOutputs(context.Background(), account, run.ID)
		if got.State != agents.StateFailed || got.OutcomeCode != "delivery_failed" || err != nil || len(out.Deliveries) != 1 ||
			out.Deliveries[0].State != api.AgentDeliveryFailed || out.Deliveries[0].Outcome.Code != tc.code {
			t.Errorf("%s: run %+v deliveries %+v %v", name, got, out.Deliveries, err)
		}
		if p := announced(t, pool, run.ID); len(p) != 1 || p[0].Result != nil {
			t.Errorf("%s: a failed run announced its result: %+v", name, p)
		}
	}
}

func TestLongCommentIsTruncated(t *testing.T) {
	_, s := deliveryFixture(t)
	body, _ := json.Marshal(map[string]string{"comment": strings.Repeat("é", api.AgentGitHubCommentMaxBytes)})
	succeed(t, s, pr7, string(body), false)
	c := claimOne(t, s)
	if len(c.Body) > api.AgentGitHubCommentMaxBytes || !strings.HasSuffix(c.Body, "(truncated by Gregale)_") || !strings.HasPrefix(c.Body, "éé") {
		t.Fatalf("body is %d bytes: ...%q", len(c.Body), c.Body[len(c.Body)-40:])
	}
}

func TestDeliveryRetriesAndUnknownOutcomes(t *testing.T) {
	pool, s := deliveryFixture(t)
	ctx := context.Background()
	run := succeed(t, s, pr7, `{"comment":"hi"}`, false)
	c := claimOne(t, s)
	if _, err := s.EndDeliveryAttempt(ctx, c, agents.DeliveryResult{Outcome: agents.DeliveryRetry, Code: "github_unavailable"}); err != nil {
		t.Fatal(err)
	}
	if claims, _, err := s.ClaimDeliveries(ctx, uuid.NewString(), time.Minute, 10); err != nil || len(claims) != 0 {
		t.Fatalf("a retry was due at once: %+v %v", claims, err)
	}
	makeDue(t, pool)
	c = claimOne(t, s)
	if c.Attempt != 2 {
		t.Fatalf("attempt %d", c.Attempt)
	}
	if _, err := s.EndDeliveryAttempt(ctx, c, agents.DeliveryResult{Outcome: agents.DeliveryUnknown, Code: "github_outcome_unknown"}); err != nil {
		t.Fatal(err)
	}
	if out, _ := s.RunOutputs(ctx, account, run.ID); out.Deliveries[0].State != api.AgentDeliveryReconciling {
		t.Fatalf("after an unknown outcome %+v", out.Deliveries)
	}
	// The window closes before the destination confirms the write.
	if _, err := pool.Exec(ctx, `UPDATE agent_deliveries SET deadline_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if claims, _, err := s.ClaimDeliveries(ctx, uuid.NewString(), time.Minute, 10); err != nil || len(claims) != 0 {
		t.Fatalf("claimed past the window: %+v %v", claims, err)
	}
	got := runState(t, s, run.ID)
	out, _ := s.RunOutputs(ctx, account, run.ID)
	if got.State != agents.StateFailed || got.OutcomeCode != "delivery_outcome_unknown" || out.Deliveries[0].State != api.AgentDeliveryOutcomeUnknown {
		t.Fatalf("run %+v deliveries %+v", got, out.Deliveries)
	}

	// A sending attempt whose worker vanished is reconciled, not retried
	// blindly, and a refusal fails at once.
	run = succeed(t, s, &agents.GitHubTarget{Repository: "acme/app", Number: 8}, `{"comment":"hi"}`, false)
	c = claimOne(t, s)
	if _, err := pool.Exec(ctx, `UPDATE agent_deliveries SET lease_expires_at = now() - interval '1 second' WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}
	makeDue(t, pool)
	again := claimOne(t, s)
	if again.ID != c.ID || again.Attempt != 2 {
		t.Fatalf("reclaim %+v", again)
	}
	if out, _ := s.RunOutputs(ctx, account, run.ID); out.Deliveries[0].State != api.AgentDeliverySending {
		t.Fatalf("reclaimed %+v", out.Deliveries)
	}
	if _, err := s.EndDeliveryAttempt(ctx, c, agents.DeliveryResult{Outcome: agents.DeliveryDelivered}); !errors.Is(err, agents.ErrLeaseLost) {
		t.Fatalf("the vanished worker recorded its attempt: %v", err)
	}
	if _, err := s.EndDeliveryAttempt(ctx, again, agents.DeliveryResult{Outcome: agents.DeliveryRejected, Code: "github_rejected",
		Message: "GitHub refused with 403"}); err != nil {
		t.Fatal(err)
	}
	if got := runState(t, s, run.ID); got.State != agents.StateFailed || got.OutcomeCode != "delivery_failed" || got.OutcomeMessage != "GitHub refused with 403" {
		t.Fatalf("refused run %+v", got)
	}
}

// A later run's comment on the same pull request replaces a waiting one, and
// never two writes to one comment run at once.
func TestDeliveriesToOneCommentAreSerialized(t *testing.T) {
	pool, s := deliveryFixture(t)
	ctx := context.Background()
	first := succeed(t, s, pr7, `{"comment":"first"}`, false)
	second := succeed(t, s, pr7, `{"comment":"second"}`, false)
	elsewhere := succeed(t, s, &agents.GitHubTarget{Repository: "ACME/app", Number: 9}, `{"comment":"elsewhere"}`, false)
	claims, _, err := s.ClaimDeliveries(ctx, uuid.NewString(), time.Minute, 10)
	if err != nil || len(claims) != 2 {
		t.Fatalf("claims %+v %v", claims, err)
	}
	byRun := map[string]agents.DeliveryClaim{claims[0].RunID: claims[0], claims[1].RunID: claims[1]}
	if _, ok := byRun[second.ID]; !ok || byRun[elsewhere.ID].Key == byRun[second.ID].Key {
		t.Fatalf("claimed %+v", claims)
	}
	if got := runState(t, s, first.ID); got.State != agents.StateSucceeded {
		t.Fatalf("the superseded run %+v", got)
	}
	if out, _ := s.RunOutputs(ctx, account, first.ID); out.Deliveries[0].State != api.AgentDeliverySuperseded {
		t.Fatalf("superseded %+v", out.Deliveries)
	}
	third := succeed(t, s, pr7, `{"comment":"third"}`, false)
	if got, _, err := s.ClaimDeliveries(ctx, uuid.NewString(), time.Minute, 10); err != nil || len(got) != 0 {
		t.Fatalf("a second write to the comment started: %+v %v", got, err)
	}
	if _, err := s.EndDeliveryAttempt(ctx, byRun[second.ID], agents.DeliveryResult{Outcome: agents.DeliveryDelivered}); err != nil {
		t.Fatal(err)
	}
	if c := claimOne(t, s); c.RunID != third.ID || c.Body != "third" {
		t.Fatalf("after the write %+v", c)
	}
	var sending int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_deliveries WHERE state = 'sending'`).Scan(&sending); err != nil || sending != 2 {
		t.Fatalf("sending %d %v", sending, err)
	}
}

func TestCancelStopsWaitingDeliveries(t *testing.T) {
	pool, s := deliveryFixture(t)
	ctx := context.Background()
	run := succeed(t, s, pr7, `{"comment":"hi"}`, false)
	if got, err := s.CancelRun(ctx, account, run.ID); err != nil || got.CancelRequestedAt == nil {
		t.Fatalf("cancel %+v %v", got, err)
	}
	if claims, _, err := s.ClaimDeliveries(ctx, uuid.NewString(), time.Minute, 10); err != nil || len(claims) != 0 {
		t.Fatalf("a cancelled run delivered: %+v %v", claims, err)
	}
	if got := runState(t, s, run.ID); got.State != agents.StateCancelled || got.FinishedAt == nil {
		t.Fatalf("run %+v", got)
	}
	if p := announced(t, pool, run.ID); len(p) != 1 || p[0].State != agents.StateCancelled || p[0].Deliveries[0].State != api.AgentDeliveryCancelled {
		t.Fatalf("announcement %+v", p)
	}
}

func TestSettleSweepEndsMissedRuns(t *testing.T) {
	pool, s := deliveryFixture(t)
	ctx := context.Background()
	run := succeed(t, s, pr7, `{"comment":"hi"}`, false)
	// The delivery ended without its run being settled.
	if _, err := pool.Exec(ctx, `UPDATE agent_deliveries SET state = 'delivered', finished_at = now()`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SettleDeliveringRuns(ctx, 10); err != nil || n != 1 {
		t.Fatalf("settled %d %v", n, err)
	}
	if got := runState(t, s, run.ID); got.State != agents.StateSucceeded {
		t.Fatalf("run %+v", got)
	}
	if n, err := s.SettleDeliveringRuns(ctx, 10); err != nil || n != 0 {
		t.Fatalf("settled again %d %v", n, err)
	}
}

const bucket = "b0c4e700-0000-4000-8000-0000000000b1"

// A bucket delivery's bucket must be the account's; the version records the
// app that owns it, and a run without kept files cannot deliver to it.
func TestBucketDeliveries(t *testing.T) {
	pool, s := deliveryFixture(t)
	ctx := context.Background()
	app := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO object_buckets (id, account_id, app_id) VALUES ($1, $2, $3), ($4, $5, $3)`,
		bucket, account, app, uuid.NewString(), other); err != nil {
		t.Fatal(err)
	}
	reporter := spec()
	reporter.Deliveries = []agents.Delivery{{Type: api.AgentDeliveryBucket, Bucket: uuid.NewString(), Prefix: "out/"}}
	if _, _, err := s.CreateAgent(ctx, account, "reporter", "", reporter, 10); !errors.Is(err, agents.ErrBucketNotFound) {
		t.Fatalf("a bucket the account lacks: %v", err)
	}
	reporter.Deliveries = []agents.Delivery{{Type: api.AgentDeliveryBucket, Bucket: bucket, Prefix: "out/"}}
	_, v, err := s.CreateAgent(ctx, account, "reporter", "", reporter, 10)
	if err != nil || len(v.Deliveries) != 1 || v.Deliveries[0].App != app || v.Deliveries[0].Prefix != "out/" {
		t.Fatalf("version %+v %v", v.Deliveries, err)
	}

	if run := finishReporter(t, s, nil); run.State != agents.StateSucceeded {
		t.Fatalf("a run without files %+v", run)
	}
	if run := finishReporter(t, s, []agents.OutputFile{{Name: "a.md", Size: 1, SHA256: "sha256:x"}}); run.State != agents.StateFailed || run.OutcomeCode != "delivery_failed" {
		t.Fatalf("files not kept %+v", run)
	}
	kept := []agents.OutputFile{{Name: "a.md", Size: 1, SHA256: "sha256:x", Key: "agents/outputs/x"}}
	run := finishReporter(t, s, kept)
	c := claimOne(t, s)
	if run.State != agents.StateDelivering || c.RunID != run.ID || c.BucketID != bucket || c.BucketAppID != app || c.Prefix != "out/" ||
		len(c.Files) != 1 || c.Files[0].Key != "agents/outputs/x" || !strings.HasPrefix(c.Key, "run-") {
		t.Fatalf("run %s claim %+v", run.State, c)
	}
}

// finishReporter drives a "reporter" run to a succeeded finish with files.
func finishReporter(t *testing.T, s *agents.Store, files []agents.OutputFile) agents.Run {
	t.Helper()
	ctx := context.Background()
	run := admit(t, s, agents.Admission{Agent: "reporter"})
	token := "tok-" + run.ID
	if _, found, err := s.ClaimRun(ctx, "agentd-1", token, ttl, maxLive(10)); err != nil || !found {
		t.Fatalf("claim %v %v", found, err)
	}
	if err := s.MarkRunning(ctx, run.ID, token, "agents-1", "agent-x", 1000); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(ctx, account, run.ID, token, agents.Finish{State: agents.StateSucceeded, Files: files}); err != nil {
		t.Fatal(err)
	}
	return runState(t, s, run.ID)
}

// An email delivery waits for apid's mail outbox receipt: agentd never
// claims it, and ends it from the receipt or its window.
func TestEmailDeliveriesEndWithTheMailReceipt(t *testing.T) {
	pool, s := deliveryFixture(t)
	ctx := context.Background()
	mailer := spec()
	mailer.Deliveries = []agents.Delivery{{Type: api.AgentDeliveryEmail}}
	if _, _, err := s.CreateAgent(ctx, account, "mailer", "", mailer, 10); err != nil {
		t.Fatal(err)
	}
	queued, queuedDelivery := finishMailer(t, pool, s)
	refused, refusedDelivery := finishMailer(t, pool, s)
	if claims, _, err := s.ClaimDeliveries(ctx, uuid.NewString(), time.Minute, 10); err != nil || len(claims) != 0 {
		t.Fatalf("agentd claimed an email: %+v %v", claims, err)
	}
	if queued.State != agents.StateDelivering {
		t.Fatalf("before the receipt %+v", queued)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO platform_mail_agent_run_observations VALUES ($1, $3, 'queued'), ($2, $3, 'unavailable')`,
		queuedDelivery, refusedDelivery, account); err != nil {
		t.Fatal(err)
	}
	_, ends, err := s.ClaimDeliveries(ctx, uuid.NewString(), time.Minute, 10)
	if err != nil || len(ends) != 2 {
		t.Fatalf("ends %+v %v", ends, err)
	}
	if got := runState(t, s, queued.ID); got.State != agents.StateSucceeded {
		t.Fatalf("queued %+v", got)
	}
	if got := runState(t, s, refused.ID); got.State != agents.StateFailed || got.OutcomeCode != "delivery_failed" {
		t.Fatalf("unavailable %+v", got)
	}
	out, _ := s.RunOutputs(ctx, account, refused.ID)
	if out.Deliveries[0].Outcome == nil || out.Deliveries[0].Outcome.Code != "mail_unavailable" {
		t.Fatalf("deliveries %+v", out.Deliveries)
	}
}

// finishMailer drives a "mailer" run to a succeeded finish with a summary.
func finishMailer(t *testing.T, pool *pgxpool.Pool, s *agents.Store) (agents.Run, string) {
	t.Helper()
	ctx := context.Background()
	run := admit(t, s, agents.Admission{Agent: "mailer"})
	token := "tok-" + run.ID
	if _, found, err := s.ClaimRun(ctx, "agentd-1", token, ttl, maxLive(10)); err != nil || !found {
		t.Fatalf("claim %v %v", found, err)
	}
	if err := s.MarkRunning(ctx, run.ID, token, "agents-1", "agent-x", 1000); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(ctx, account, run.ID, token, agents.Finish{State: agents.StateSucceeded,
		Result: json.RawMessage(`{"summary":"Two PRs need review."}`)}); err != nil {
		t.Fatal(err)
	}
	var delivery, body string
	if err := pool.QueryRow(ctx, `SELECT id::text, body FROM agent_deliveries WHERE run_id=$1`, run.ID).Scan(&delivery, &body); err != nil || body != "Two PRs need review." {
		t.Fatalf("delivery %q %v", body, err)
	}
	return runState(t, s, run.ID), delivery
}
