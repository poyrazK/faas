// adr: 20261027090010
package mailoutbox

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

func agentRunFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	_, path := verificationIdentity(t)
	f.env[internalmail.AgentRunDeliveredOutboxEnableEnv], f.env[VerificationIdentityEnv] = "1", path
	if err := f.service.ConfigureAgentRunDelivered(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	f.service.agentRunDelivered.sender = f.sender
	return f
}

// emailDelivery is agentd's record of a finished run's email delivery.
func emailDelivery(t *testing.T, f *fixture, accountID, state, body string) string {
	t.Helper()
	agent, run, id := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO agents(id,account_id,name) VALUES($1,$2,'triage')`, agent, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO agent_runs(id,account_id,agent_id) VALUES($1,$2,$3)`, run, accountID, agent); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO agent_deliveries(id,account_id,run_id,destination,state,body) VALUES($1,$2,$3,'email',$4,$5)`,
		id, accountID, run, state, body); err != nil {
		t.Fatal(err)
	}
	return id
}

func receipt(t *testing.T, f *fixture, delivery string) (string, string) {
	t.Helper()
	var disposition, intent string
	if err := f.pool.QueryRow(t.Context(), `SELECT disposition,coalesce(intent_id::text,'') FROM platform_mail_agent_run_observations WHERE delivery_id=$1`,
		delivery).Scan(&disposition, &intent); err != nil {
		t.Fatal(err)
	}
	return disposition, intent
}

func TestAgentRunDeliveredDisabledReadsOnlyOwnGate(t *testing.T) {
	var s *Service
	for _, value := range []string{"", "0", "true", " 1"} {
		if err := s.ConfigureAgentRunDelivered(t.Context(), func(k string) string {
			if k != internalmail.AgentRunDeliveredOutboxEnableEnv {
				t.Fatal(k)
			}
			return value
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPGAgentRunDeliveredCapturesSealsAndSends(t *testing.T) {
	f := agentRunFixture(t)
	id := account(t, f)
	secret := "Authorization: Bearer abc.def.ghi"
	delivery := emailDelivery(t, f, id, "pending", "Two PRs need review.\n"+secret+"\n"+strings.Repeat("x", 2000))
	if err := f.service.CaptureAgentRunDeliveries(t.Context()); err != nil {
		t.Fatal(err)
	}
	disposition, intentID := receipt(t, f, delivery)
	if disposition != "queued" || intentID == "" {
		t.Fatalf("receipt %s %s", disposition, intentID)
	}
	intent := row(t, f, intentID)
	if intent.Template != AgentRunDeliveredTemplate || intent.RecipientAddress != "one@example.net" || uuidString(intent.AgentDeliveryID.Bytes) != delivery ||
		strings.Contains(string(intent.SealedSecurityAlert), "Two PRs") {
		t.Fatalf("intent %+v", intent)
	}
	payload, err := agentRunDeliveredPayloadFor(f.service.agentRunDelivered, intent)
	if err != nil || !payload.Truncated || strings.Contains(payload.Summary, "abc.def.ghi") || !strings.HasPrefix(payload.Summary, "Two PRs need review.") ||
		payload.Agent != "triage" || !intent.ExpiresAt.Time.Equal(payload.FinishedAt.Add(24*time.Hour)) {
		t.Fatalf("payload %+v %v", payload, err)
	}
	for _, mutate := range []func(*q.PlatformMailProducerOutbox){
		func(r *q.PlatformMailProducerOutbox) { r.AgentDeliveryID = pgID(uuid.NewString()) },
		func(r *q.PlatformMailProducerOutbox) { r.RecipientAddress = "another@example.net" },
		func(r *q.PlatformMailProducerOutbox) { r.ExpiresAt.Time = r.ExpiresAt.Time.Add(time.Second) },
	} {
		copy := intent
		mutate(&copy)
		if _, err := agentRunDeliveredPayloadFor(f.service.agentRunDelivered, copy); err == nil {
			t.Fatal("payload metadata substitution accepted")
		}
	}
	// A second pass captures nothing again.
	if err := f.service.CaptureAgentRunDeliveries(t.Context()); err != nil {
		t.Fatal(err)
	}
	var intents int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM platform_mail_producer_outbox WHERE agent_delivery_id=$1`, delivery).Scan(&intents); err != nil || intents != 1 {
		t.Fatalf("intents %d %v", intents, err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.sender.calls) != 1 {
		t.Fatalf("sent %d", len(f.sender.calls))
	}
	m := f.sender.calls[0].message
	if m.To[0] != "one@example.net" || m.Subject != "Agent triage finished a run" || !strings.Contains(m.TextBody, "Two PRs need review.") ||
		strings.Contains(m.TextBody, "abc.def.ghi") || !strings.Contains(m.TextBody, "shortened") || m.MessageID != "agent-run-delivered:"+intentID {
		t.Fatalf("message %+v", m)
	}
	assertMFAAlertErased(t, row(t, f, intentID))
}

func TestPGAgentRunDeliveredSkipsWhatCannotBeMailed(t *testing.T) {
	f := agentRunFixture(t)
	active := account(t, f)
	stale := emailDelivery(t, f, active, "cancelled", "late")
	suspended := uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO accounts(id,email,status) VALUES($1,'two@example.net','suspended')", suspended); err != nil {
		t.Fatal(err)
	}
	unavailable := emailDelivery(t, f, suspended, "pending", "hi")
	closing := emailDelivery(t, f, active, "pending", "hi")
	if _, err := f.pool.Exec(t.Context(), "UPDATE agent_deliveries SET deadline_at=now()+interval '30 seconds' WHERE id=$1", closing); err != nil {
		t.Fatal(err)
	}
	if err := f.service.CaptureAgentRunDeliveries(t.Context()); err != nil {
		t.Fatal(err)
	}
	for delivery, want := range map[string]string{stale: "stale", unavailable: "unavailable", closing: "expired"} {
		if got, intent := receipt(t, f, delivery); got != want || intent != "" {
			t.Errorf("%s: %s %s, want %s", delivery, got, intent, want)
		}
	}
	// A run that finished before the template was enabled is never mailed.
	if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_agent_run_capture SET started_at=clock_timestamp()"); err != nil {
		t.Fatal(err)
	}
	earlier := emailDelivery(t, f, active, "pending", "hi")
	if _, err := f.pool.Exec(t.Context(), "UPDATE agent_deliveries SET created_at=now()-interval '1 minute' WHERE id=$1", earlier); err != nil {
		t.Fatal(err)
	}
	if err := f.service.CaptureAgentRunDeliveries(t.Context()); err != nil {
		t.Fatal(err)
	}
	var observed bool
	if err := f.pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM platform_mail_agent_run_observations WHERE delivery_id=$1)`, earlier).Scan(&observed); err != nil || observed {
		t.Fatalf("observed %v %v", observed, err)
	}
	// The account changes before dispatch: the intent is cancelled.
	pending := emailDelivery(t, f, active, "pending", "hi")
	if err := f.service.CaptureAgentRunDeliveries(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, intent := receipt(t, f, pending)
	if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET status='suspended' WHERE id=$1", active); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, intent); got.State != "cancelled" || len(f.sender.calls) != 0 {
		t.Fatalf("intent %s sent %d", got.State, len(f.sender.calls))
	}
}
