package mailoutbox

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/mail"
)

const testProducerToken = "private-outbox-producer-test-credential-32-bytes"

func testEnv() map[string]string {
	return map[string]string{
		internalmail.EnableEnv: "1", internalmail.ProducerOutboxEnableEnv: "1", internalmail.ProducersEnableEnv: "1", internalmail.SchedulingEnableEnv: "1",
		"FAAS_MAIL_TRANSPORT": "internal-producer", "FAAS_MAIL_FROM": "Gregale <notify@example.test>", "FAAS_INTERNAL_MAIL_PRODUCER_ID": "auth", "FAAS_INTERNAL_MAIL_PRODUCER_TOKEN": testProducerToken,
	}
}

func migrationFileParts(t *testing.T, name string) (string, string) {
	t.Helper()
	raw, err := migrations.FS.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose StatementBegin\n")
	return strings.Split(parts[1], "-- +goose StatementEnd")[0], strings.Split(parts[2], "-- +goose StatementEnd")[0]
}

// migrationSectionParts returns the raw Up and Down sections of a migration
// whose Up is plain statements rather than one StatementBegin block.
func migrationSectionParts(t *testing.T, name string) (string, string) {
	t.Helper()
	raw, err := migrations.FS.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	_, rest, okUp := strings.Cut(string(raw), "-- +goose Up\n")
	up, down, okDown := strings.Cut(rest, "-- +goose Down\n")
	if !okUp || !okDown {
		t.Fatalf("%s has no Up and Down sections", name)
	}
	return up, down
}

func migrationParts(t *testing.T) (string, string) {
	t.Helper()
	up, down := migrationFileParts(t, "20261016183431589_platform_mail_producer_outbox.sql")
	reconcileUp, reconcileDown := migrationFileParts(t, "20261016192204806_platform_mail_outbox_reconciliation.sql")
	verificationUp, verificationDown := migrationFileParts(t, "20261016195608821_platform_mail_verification_outbox.sql")
	resetUp, resetDown := migrationFileParts(t, "20261017073748949_platform_mail_password_reset_outbox.sql")
	magicUp, magicDown := migrationFileParts(t, "20261017082032672_platform_mail_magic_link_outbox.sql")
	alertUp, alertDown := migrationFileParts(t, "20261017084607182_platform_mail_mfa_recovery_alert_outbox.sql")
	disableUp, disableDown := migrationFileParts(t, "20261017091735652_platform_mail_mfa_disable_outbox.sql")
	disabledUp, disabledDown := migrationFileParts(t, "20261017094937523_platform_mail_mfa_disabled_alert_outbox.sql")
	changedUp, changedDown := migrationFileParts(t, "20261017101020256_platform_mail_password_changed_alert_outbox.sql")
	enabledUp, enabledDown := migrationFileParts(t, "20261017102802761_platform_mail_mfa_enabled_alert_outbox.sql")
	paymentUp, paymentDown := migrationFileParts(t, "20261017105351033_platform_mail_payment_failed_outbox.sql")
	restoredUp, restoredDown := migrationFileParts(t, "20261017112923185_platform_mail_account_restored_outbox.sql")
	suspendedUp, suspendedDown := migrationFileParts(t, "20261017115753852_platform_mail_account_suspended_outbox.sql")
	nonpaymentUp, nonpaymentDown := migrationFileParts(t, "20261017125602853_platform_mail_nonpayment_deletion_outbox.sql")
	quotaUp, quotaDown := migrationFileParts(t, "20261017133739854_platform_mail_quota_warning_outbox.sql")
	endedUp, endedDown := migrationFileParts(t, "20261017142138994_platform_mail_subscription_ended_outbox.sql")
	deployUp, deployDown := migrationFileParts(t, "20261017155842001_platform_mail_deployment_failed_outbox.sql")
	scopeShapeUp, scopeShapeDown := migrationSectionParts(t, "20261018162329975_platform_mail_deployment_scope_shape.sql")
	agentUp, agentDown := migrationFileParts(t, "20261027141052602_platform_mail_agent_run_delivered_outbox.sql")
	return up + "\n" + reconcileUp + "\n" + verificationUp + "\n" + resetUp + "\n" + magicUp + "\n" + alertUp + "\n" + disableUp + "\n" + disabledUp + "\n" + changedUp + "\n" + enabledUp + "\n" + paymentUp + "\n" + restoredUp + "\n" + suspendedUp + "\n" + nonpaymentUp + "\n" + quotaUp + "\n" + endedUp + "\n" + deployUp + "\n" + scopeShapeUp + "\n" + agentUp, agentDown + "\n" + scopeShapeDown + "\n" + deployDown + "\n" + endedDown + "\n" + quotaDown + "\n" + nonpaymentDown + "\n" + suspendedDown + "\n" + restoredDown + "\n" + paymentDown + "\n" + enabledDown + "\n" + changedDown + "\n" + disabledDown + "\n" + disableDown + "\n" + alertDown + "\n" + magicDown + "\n" + resetDown + "\n" + verificationDown + "\n" + reconcileDown + "\n" + down
}

func outboxSchema(t *testing.T) (string, string) {
	t.Helper()
	projection, err := os.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _ := migrationParts(t)
	if !strings.HasSuffix(string(projection), up) {
		t.Fatal("outbox sqlc schema differs from migration")
	}
	return string(projection), up
}

// Check source drift even when the PostgreSQL fixtures are skipped locally.
func TestSchemaProjectionMatchesMigrations(t *testing.T) {
	outboxSchema(t)
}

type fixture struct {
	service *Service
	pool    *pgxpool.Pool
	env     map[string]string
	sender  *fakeSender
}

// Billing lifecycle fixtures also exercise the real state store. Its organization
// lookup needs these policy columns even when the account is personal.
func addOrgBillingPolicyFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `ALTER TABLE orgs
ADD COLUMN resource_ownership_enabled boolean NOT NULL DEFAULT false,
ADD COLUMN deleted_pending boolean NOT NULL DEFAULT false,
ADD COLUMN created_at timestamptz NOT NULL DEFAULT now(),
ADD COLUMN past_due_at timestamptz,
ADD COLUMN last_quota_warning_at timestamptz,
ADD COLUMN suspended_reason text,
ADD COLUMN abuse_hold_at timestamptz,
ADD COLUMN abuse_hold_reason text,
ADD COLUMN billing_email text NOT NULL DEFAULT '',
ADD COLUMN business_name text NOT NULL DEFAULT '',
ADD COLUMN billing_address text NOT NULL DEFAULT '',
ADD COLUMN tax_id text NOT NULL DEFAULT ''`); err != nil {
		t.Fatal(err)
	}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := pgtest.Open(t)
	projection, up := outboxSchema(t)
	if _, err := pool.Exec(t.Context(), projection); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), up); err != nil {
		t.Fatal("migration replay", err)
	}
	addOrgBillingPolicyFixture(t, pool)
	env := testEnv()
	service, err := NewFromEnv(pool, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	sender := &fakeSender{}
	service.sender = sender
	return &fixture{service, pool, env, sender}
}

func account(t *testing.T, f *fixture) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO accounts(id,email,status) VALUES($1,'one@example.net','active')", id); err != nil {
		t.Fatal(err)
	}
	return id
}

func schedule(t *testing.T, f *fixture, id string) q.PlatformMailProducerOutbox {
	t.Helper()
	if err := f.service.ScheduleDeletion(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	var intentID string
	if err := f.pool.QueryRow(t.Context(), "SELECT id::text FROM platform_mail_producer_outbox WHERE account_id=$1 ORDER BY created_at DESC LIMIT 1", id).Scan(&intentID); err != nil {
		t.Fatal(err)
	}
	r, err := f.service.queries.Intent(t.Context(), f.pool, q.IntentParams{Namespace: "platform", ID: pgID(intentID)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func row(t *testing.T, f *fixture, id string) q.PlatformMailProducerOutbox {
	t.Helper()
	r, err := f.service.queries.Intent(t.Context(), f.pool, q.IntentParams{Namespace: "platform", ID: pgID(id)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

type captured struct {
	message mail.Message
	timing  internalmail.MessageTiming
}
type fakeSender struct {
	mu    sync.Mutex
	calls []captured
	send  func(context.Context, mail.Message, internalmail.MessageTiming) error
}

func (s *fakeSender) Send(ctx context.Context, m mail.Message) error {
	return s.SendTimed(ctx, m, internalmail.MessageTiming{})
}
func (s *fakeSender) SendTimedWithRecovery(ctx context.Context, m mail.Message, timing internalmail.MessageTiming, generation string) error {
	if !internalmail.ValidProducerOutboxGeneration(generation) {
		return internalmail.ErrInvalid
	}
	return s.SendTimed(ctx, m, timing)
}
func (s *fakeSender) SendTimed(ctx context.Context, m mail.Message, timing internalmail.MessageTiming) error {
	s.mu.Lock()
	s.calls = append(s.calls, captured{m, timing})
	s.mu.Unlock()
	if s.send != nil {
		return s.send(ctx, m, timing)
	}
	return nil
}

func TestProducerOutboxDefaultOffReadsNoConfiguration(t *testing.T) {
	for _, flag := range []string{"", "0", "true", " 1", "1 "} {
		s, err := NewFromEnv(nil, func(k string) string {
			if k != internalmail.ProducerOutboxEnableEnv {
				t.Fatal("disabled outbox read private config", k)
			}
			return flag
		})
		if s != nil || err != nil {
			t.Fatal("disabled outbox constructed")
		}
	}
	for _, key := range []string{internalmail.EnableEnv, internalmail.ProducersEnableEnv, internalmail.SchedulingEnableEnv, "FAAS_MAIL_TRANSPORT"} {
		env := testEnv()
		env[key] = "0"
		if _, err := NewFromEnv(nil, func(k string) string { return env[k] }); err == nil {
			t.Fatal("missing prerequisite accepted", key)
		}
	}
	s := &Service{}
	if err := s.DeliverOne(t.Context()); err != nil {
		t.Fatal("disabled worker used pool", err)
	}
	if err := s.ScheduleDeletion(t.Context(), uuid.NewString()); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal(err)
	}
}

func TestPGProducerOutboxAtomicSaveConcurrentReplayAndRestart(t *testing.T) {
	f := newFixture(t)
	id := account(t, f)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			if err := f.service.ScheduleDeletion(t.Context(), id); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var count int
	var status string
	var requestedAt time.Time
	if err := f.pool.QueryRow(t.Context(), "SELECT status,deletion_requested_at FROM accounts WHERE id=$1", id).Scan(&status, &requestedAt); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM platform_mail_producer_outbox WHERE account_id=$1", id).Scan(&count); err != nil || count != 1 || status != "deleted_pending" {
		t.Fatal("transition split or duplicate intent", err)
	}
	if len(f.sender.calls) != 0 {
		t.Fatal("business transaction performed HTTP")
	}
	before := schedule(t, f, id)
	if !before.DeletionRequestedAt.Time.Equal(requestedAt) || !before.RestoreUntil.Time.Equal(requestedAt.Add(api.AccountDeletionGraceDays*24*time.Hour)) {
		t.Fatal("grace anchor changed")
	}
	restarted, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	restarted.sender = f.sender
	if err := restarted.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	after := row(t, f, uuid.UUID(before.ID.Bytes).String())
	if after.State != "admitted" || after.Attempts != 1 || after.RecipientAddress != "" || len(f.sender.calls) != 1 {
		t.Fatal("restart lost handoff or retained recipient unnecessarily")
	}
	if f.sender.calls[0].message.MessageID != "account-delete:"+uuid.UUID(before.ID.Bytes).String() || !f.sender.calls[0].timing.ExpiresAt.Equal(before.ExpiresAt.Time) {
		t.Fatal("handoff lacks stable ID/deadline")
	}
}

func TestPGProducerOutboxFailureRollsBackBusinessChange(t *testing.T) {
	f := newFixture(t)
	id := account(t, f)
	if _, err := f.pool.Exec(t.Context(), "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT test_injected_failure CHECK (attempts<0)"); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ScheduleDeletion(t.Context(), id); err == nil {
		t.Fatal("injected save failure accepted")
	}
	var status string
	var missing bool
	if err := f.pool.QueryRow(t.Context(), "SELECT status,deletion_requested_at IS NULL FROM accounts WHERE id=$1", id).Scan(&status, &missing); err != nil || status != "active" || !missing {
		t.Fatal("business change committed without intent", err)
	}
	if _, err := f.pool.Exec(t.Context(), "ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT test_injected_failure"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	f.service.enabled = func() bool { calls++; return calls == 1 }
	if err := f.service.ScheduleDeletion(t.Context(), id); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("late disable committed intent", err)
	}
	if err := f.pool.QueryRow(t.Context(), "SELECT status FROM accounts WHERE id=$1", id).Scan(&status); err != nil || status != "active" {
		t.Fatal("late disable committed account transition", err)
	}
}

func TestPGProducerOutboxLostAcknowledgementRetriesImmutableIntent(t *testing.T) {
	f := newFixture(t)
	intent := schedule(t, f, account(t, f))
	id := uuid.UUID(intent.ID.Bytes).String()
	var accepted captured
	calls := 0
	f.sender.send = func(_ context.Context, m mail.Message, timing internalmail.MessageTiming) error {
		calls++
		if calls == 1 {
			accepted = captured{m, timing}
			return &mail.TransientError{Err: errors.New("lost admission acknowledgement")}
		}
		if !reflect.DeepEqual(accepted, captured{m, timing}) {
			t.Error("retry changed admitted payload")
		}
		return nil
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, id); got.State != "queued" || got.Code != "maild_unavailable" || got.RecipientAddress == "" {
		t.Fatal("ambiguous admission was discarded")
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, id); got.State != "admitted" || calls != 2 {
		t.Fatal("stable handoff did not resume")
	}
}

func TestPGProducerOutboxRestorationExpirySuppressionAndRearming(t *testing.T) {
	for _, scenario := range []string{"restored", "changed-email", "expired", "suppressed", "rearmed"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			accountID := account(t, f)
			intent := schedule(t, f, accountID)
			id := uuid.UUID(intent.ID.Bytes).String()
			var command string
			switch scenario {
			case "restored", "rearmed":
				command = "UPDATE accounts SET status='active',deletion_requested_at=NULL WHERE id=$1"
			case "changed-email":
				command = "UPDATE accounts SET email='other@example.net' WHERE id=$1"
			case "expired":
				command = "UPDATE platform_mail_producer_outbox SET deletion_requested_at=clock_timestamp()-interval '2 days',expires_at=clock_timestamp()-interval '1 hour' WHERE account_id=$1"
			case "suppressed":
				command = "INSERT INTO mail_suppressions(email) SELECT email FROM accounts WHERE id=$1"
			}
			if _, err := f.pool.Exec(t.Context(), command, accountID); err != nil {
				t.Fatal(err)
			}
			if scenario == "rearmed" {
				schedule(t, f, accountID)
			}
			if err := f.service.DeliverOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			got := row(t, f, id)
			want := "cancelled"
			if scenario == "expired" {
				want = "expired"
			}
			if scenario == "suppressed" {
				want = "suppressed"
			}
			if got.State != want || got.RecipientAddress != "" || len(f.sender.calls) != 0 {
				t.Fatal("stale/suppressed notice handed off", got.State)
			}
		})
	}
}

func TestPGProducerOutboxRecoveryGenerationAndConfigChangesDoNotResubmit(t *testing.T) {
	f := newFixture(t)
	intent := schedule(t, f, account(t, f))
	id := uuid.UUID(intent.ID.Bytes).String()
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO internal_mail_recovery_state(namespace,recovery_id,held) VALUES('platform',$1,true)", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recovery_state SET held=false"); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, id); got.Attempts != 0 || got.State != "queued" || len(f.sender.calls) != 0 {
		t.Fatal("global release replayed old producer intent")
	}
	if _, err := f.pool.Exec(t.Context(), "DELETE FROM internal_mail_recovery_state"); err != nil {
		t.Fatal(err)
	}
	f.service.producer = "other"
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.sender.calls) != 0 {
		t.Fatal("new producer took old intent")
	}
	f.service.producer = "auth"
	f.service.from = "other@example.test"
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.sender.calls) != 0 {
		t.Fatal("changed From changed a retry payload")
	}
}

func TestPGProducerOutboxLeaseFencesAndConcurrentWorkers(t *testing.T) {
	f := newFixture(t)
	intent := schedule(t, f, account(t, f))
	id := uuid.UUID(intent.ID.Bytes).String()
	old, err := f.service.claim(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	newClaim, err := f.service.claim(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.dispatch(t.Context(), old); !errors.Is(err, internalmail.ErrLeaseLost) {
		t.Fatal("stale claimant submitted", err)
	}
	if err := f.service.dispatch(t.Context(), newClaim); err != nil {
		t.Fatal(err)
	}
	if len(f.sender.calls) != 1 {
		t.Fatal("lease fencing duplicated handoff")
	}
	for range 4 {
		schedule(t, f, account(t, f))
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := f.service.DeliverOne(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if len(f.sender.calls) != 5 {
		t.Fatal("concurrent workers lost or duplicated intents")
	}
}

func TestPGProducerOutboxPrivateMetadataAndAccountDeletionCascade(t *testing.T) {
	f := newFixture(t)
	accountID := account(t, f)
	intent := schedule(t, f, accountID)
	_, down := migrationParts(t)
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded saved intent")
	}
	if _, err := f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", accountID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM platform_mail_producer_outbox WHERE id=$1", intent.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("account erasure retained recipient snapshot", err)
	}
	if _, err := f.pool.Exec(t.Context(), down); err != nil {
		t.Fatal("empty rollback refused", err)
	}
}

func TestPGProducerOutboxPreflightDisablePreservesIntentAndAttemptBudget(t *testing.T) {
	f := newFixture(t)
	intent := schedule(t, f, account(t, f))
	f.sender.send = func(context.Context, mail.Message, internalmail.MessageTiming) error { return internalmail.ErrDisabled }
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := row(t, f, uuid.UUID(intent.ID.Bytes).String())
	if got.State != "queued" || got.Attempts != 0 || got.RecipientAddress == "" {
		t.Fatal("preflight disable discarded intent or charged an attempt")
	}
}

func TestPGProducerOutboxPendingBoundRollsBackDeletion(t *testing.T) {
	f := newFixture(t)
	owner, target := account(t, f), account(t, f)
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO platform_mail_producer_outbox(id,account_id,namespace,producer_id,from_address,recipient_address,
        template,template_version,deletion_requested_at,restore_until,expires_at)
        SELECT gen_random_uuid(),$1,'platform','auth','notify@example.test','one@example.net','account_deletion_pending',1,
            clock_timestamp()-n*interval '1 second',clock_timestamp()+interval '30 days',clock_timestamp()+interval '1 day'
        FROM generate_series(1,$2::integer) n`, owner, api.InternalMailProducerOutboxMaxPending); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ScheduleDeletion(t.Context(), target); !errors.Is(err, internalmail.ErrQuota) {
		t.Fatal("pending bound did not reject atomic operation", err)
	}
	var status string
	if err := f.pool.QueryRow(t.Context(), "SELECT status FROM accounts WHERE id=$1", target).Scan(&status); err != nil || status != "active" {
		t.Fatal("full outbox still committed deletion", err)
	}
}

func TestPGProducerOutboxRestoreWaitsForHandoffBoundary(t *testing.T) {
	f := newFixture(t)
	accountID := account(t, f)
	schedule(t, f, accountID)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	f.sender.send = func(ctx context.Context, _ mail.Message, _ internalmail.MessageTiming) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	done := make(chan error, 1)
	go func() { done <- f.service.DeliverOne(ctx) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("handoff never started")
	}
	var restored atomic.Bool
	restoredDone := make(chan error, 1)
	go func() {
		_, err := f.pool.Exec(ctx, "UPDATE accounts SET status='active',deletion_requested_at=NULL WHERE id=$1", accountID)
		restored.Store(true)
		restoredDone <- err
	}()
	// A short database-side lock timeout proves the account is held while
	// the external handoff is active, without relying on sleep scheduling.
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout='100ms'"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SELECT id FROM accounts WHERE id=$1 FOR UPDATE", accountID); err == nil {
		t.Fatal("restore crossed active handoff")
	}
	_ = tx.Rollback(ctx)
	if restored.Load() {
		t.Fatal("account restoration committed during handoff")
	}
	unblock()
	for _, ch := range []chan error{done, restoredDone} {
		select {
		case err := <-ch:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("handoff boundary did not drain")
		}
	}
}
