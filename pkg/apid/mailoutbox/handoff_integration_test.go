package mailoutbox

import (
	"context"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/state"
)

type fixtureTXT map[string][]string

func (r fixtureTXT) LookupTXT(_ context.Context, name string) ([]string, error) { return r[name], nil }

type fixtureSMTP struct{ accepted atomic.Int32 }

func (s *fixtureSMTP) Submit(context.Context, internalmail.Envelope) (string, string) {
	s.accepted.Add(1)
	return "submitted", "relay_accepted"
}

func TestPGProducerOutboxRealMaildLostAcknowledgementDoesNotDuplicate(t *testing.T) {
	pool := pgtest.Open(t)
	if _, err := pool.Exec(t.Context(), `CREATE TABLE accounts(id uuid PRIMARY KEY,email text NOT NULL,status text NOT NULL,plan text NOT NULL DEFAULT 'free',provider_customer_id text,stripe_subscription_item text,last_quota_warning_at timestamptz,deletion_requested_at timestamptz,email_verified_at timestamptz,abuse_hold_at timestamptz,mfa_enrolled_at timestamptz,mfa_recovery_codes_hash bytea[],mfa_secret_encrypted bytea,mfa_required boolean NOT NULL DEFAULT false,past_due_at timestamptz,suspended_reason text);
CREATE TABLE usage_minutes(account_id uuid NOT NULL,minute timestamptz NOT NULL,mb_seconds bigint NOT NULL);
CREATE TABLE login_tokens(token_hash bytea PRIMARY KEY,account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,expires_at timestamptz NOT NULL,consumed_at timestamptz);
CREATE TABLE mfa_disable_requests(token_hash bytea PRIMARY KEY,account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,requested_at timestamptz NOT NULL,consumed_at timestamptz);
CREATE TABLE email_verification_tokens(token_hash bytea PRIMARY KEY,account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,expires_at timestamptz NOT NULL,consumed_at timestamptz,created_at timestamptz NOT NULL DEFAULT now());
        CREATE TABLE account_passwords(account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,hash text NOT NULL,updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE orgs(id uuid PRIMARY KEY,personal_org boolean NOT NULL DEFAULT false,personal_owner_account_id uuid,status text NOT NULL DEFAULT 'active',plan text NOT NULL DEFAULT 'free',provider_customer_id text,stripe_subscription_item text,updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE billing_identities(account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,provider text NOT NULL,customer_id text NOT NULL,subscription_id text NOT NULL DEFAULT '',billing_from timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(account_id,provider),UNIQUE(provider,customer_id));
CREATE TABLE apps(id uuid PRIMARY KEY,account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,streaming_enabled boolean NOT NULL DEFAULT false,slug text NOT NULL DEFAULT 'test-app',status text NOT NULL DEFAULT 'active',org_id uuid,project_id uuid,last_deploy_failed_email_at timestamptz);
CREATE TABLE deployments(id uuid PRIMARY KEY,app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,status text NOT NULL DEFAULT 'pending',scope text NOT NULL DEFAULT 'default',deployed_by_user_id uuid REFERENCES accounts(id) ON DELETE SET NULL,deleted_at timestamptz,created_at timestamptz NOT NULL DEFAULT now(),error_code text,error text,error_hint text,error_why text,error_fix text);
CREATE TABLE org_memberships(org_id uuid NOT NULL,account_id uuid NOT NULL,role text NOT NULL,removed_at timestamptz,PRIMARY KEY(org_id,account_id));
CREATE TABLE mail_suppressions(email text NOT NULL,expires_at timestamptz)`); err != nil {
		t.Fatal(err)
	}
	names, err := fs.Glob(migrations.FS, "*_internal_mail_*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		raw, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		up := strings.Split(strings.Split(string(raw), "-- +goose StatementBegin\n")[1], "-- +goose StatementEnd")[0]
		if _, err := pool.Exec(t.Context(), up); err != nil {
			t.Fatal(name, err)
		}
	}
	up, _ := migrationParts(t)
	if _, err := pool.Exec(t.Context(), up); err != nil {
		t.Fatal(err)
	}
	store, err := internalmail.NewPGStore(pool, "platform")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	resolver := fixtureTXT{}
	smtp := &fixtureSMTP{}
	const serviceToken = "private-maild-service-integration-token-32-bytes"
	const operatorToken = "private-maild-operator-integration-token-32-bytes"
	var outboxGate atomic.Bool
	var reconciliationGate atomic.Bool
	var verificationGate atomic.Bool
	var passwordResetGate atomic.Bool
	var magicLinkGate atomic.Bool
	var mfaAlertGate atomic.Bool
	var mfaDisableGate atomic.Bool
	var mfaDisabledAlertGate atomic.Bool
	var passwordChangedAlertGate atomic.Bool
	var nonpaymentDeletionGate atomic.Bool
	var quotaWarningGate atomic.Bool
	var subscriptionEndedGate atomic.Bool
	var deploymentFailedGate atomic.Bool
	var accountSuspendedGate atomic.Bool
	var accountRestoredGate atomic.Bool
	var paymentFailedGate atomic.Bool
	var mfaEnabledAlertGate atomic.Bool
	outboxGate.Store(true)
	daemon := &internalmail.Service{Store: store, Codec: internalmail.AgeCodec{Identity: identity, Namespace: "platform"}, Resolver: resolver, SendingIP: netip.MustParseAddr("192.0.2.10"),
		Enabled: func() bool { return true }, ProducersEnabled: func() bool { return true }, SchedulingEnabled: func() bool { return true }, DeliveryEnabled: func() bool { return true },
		ProducerOutboxEnabled: outboxGate.Load, OperationsEnabled: func() bool { return true }, OperationsToken: operatorToken,
		OutboxReconciliationEnabled:       reconciliationGate.Load,
		VerificationOutboxEnabled:         verificationGate.Load,
		PasswordResetOutboxEnabled:        passwordResetGate.Load,
		MagicLinkOutboxEnabled:            magicLinkGate.Load,
		MFARecoveryAlertOutboxEnabled:     mfaAlertGate.Load,
		MFADisableOutboxEnabled:           mfaDisableGate.Load,
		MFADisabledAlertOutboxEnabled:     mfaDisabledAlertGate.Load,
		PasswordChangedAlertOutboxEnabled: passwordChangedAlertGate.Load,
		NonpaymentDeletionOutboxEnabled:   nonpaymentDeletionGate.Load,
		QuotaWarningOutboxEnabled:         quotaWarningGate.Load,
		SubscriptionEndedOutboxEnabled:    subscriptionEndedGate.Load,
		DeploymentFailedOutboxEnabled:     deploymentFailedGate.Load,
		AccountSuspendedOutboxEnabled:     accountSuspendedGate.Load,
		AccountRestoredOutboxEnabled:      accountRestoredGate.Load,
		PaymentFailedOutboxEnabled:        paymentFailedGate.Load,
		MFAEnabledAlertOutboxEnabled:      mfaEnabledAlertGate.Load,
		Producers:                         map[string]internalmail.Producer{"auth": {ID: "auth", Token: testProducerToken, From: []string{"notify@example.test"}, DailyRecipientLimit: 100, PendingRecipientLimit: 100}},
		RecipientAllowed:                  func(string) bool { return true }, Transport: smtp}
	domain, err := daemon.RegisterDomain(t.Context(), "example.test")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range domain.Records {
		resolver[record.Name] = []string{record.Value}
	}
	if _, err := daemon.VerifyDomain(t.Context(), domain.ID); err != nil {
		t.Fatal(err)
	}
	handler, err := internalmail.Handler(daemon, serviceToken)
	if err != nil {
		t.Fatal(err)
	}
	var dropped atomic.Bool
	var recoverBeforeAdmission atomic.Bool
	recoveryDone := make(chan error, 1)
	bridge := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && recoverBeforeAdmission.CompareAndSwap(true, false) {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			err := store.ConfigureRecovery(ctx, uuid.NewString(), func() bool { return true })
			if err == nil {
				_, err = pool.Exec(ctx, "UPDATE internal_mail_recovery_state SET held=false WHERE namespace='platform'")
			}
			recoveryDone <- err
			if err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages") && dropped.CompareAndSwap(false, true) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, r)
			if response.Code != http.StatusAccepted {
				t.Error("maild did not accept before connection loss", response.Code)
			}
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = connection.Close()
			return
		}
		handler.ServeHTTP(w, r)
	})
	directory, err := os.MkdirTemp("/tmp", "mail-outbox-unix-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "maild.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: bridge}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close(); <-done })
	env := testEnv()
	env["FAAS_INTERNAL_MAIL_SOCKET"] = socket
	producer, err := NewFromEnv(pool, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{service: producer, pool: pool, env: env}
	accountID := account(t, f)
	intent := schedule(t, f, accountID)
	id := uuid.UUID(intent.ID.Bytes).String()
	if err := producer.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, id); got.State != "queued" || got.Code != "maild_unavailable" {
		t.Fatal("lost acknowledgement discarded intent")
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM internal_mail_messages WHERE namespace='platform' AND producer_id='auth'").Scan(&count); err != nil || count != 1 {
		t.Fatal("maild acceptance was not durable", err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewFromEnv(pool, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, id); got.State != "admitted" || got.Attempts != 2 {
		t.Fatal("restart failed to acknowledge original acceptance")
	}
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM internal_mail_messages WHERE namespace='platform' AND producer_id='auth'").Scan(&count); err != nil || count != 1 {
		t.Fatal("retry duplicated maild admission", err)
	}
	for range 2 {
		if err := daemon.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if smtp.accepted.Load() != 1 {
		t.Fatal("lost producer acknowledgement caused duplicate SMTP handoff")
	}
	for _, token := range []string{serviceToken, testProducerToken, operatorToken} {
		r := httptest.NewRequest(http.MethodGet, internalmail.ProducerOutboxPath+"/items", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if token != operatorToken {
			if w.Code != 401 {
				t.Fatal("nonoperator read producer outbox", w.Code)
			}
			continue
		}
		if w.Code != 200 {
			t.Fatal("private outbox inspection failed", w.Code)
		}
		var page internalmail.OperatorPage[internalmail.ProducerOutboxItem]
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].State != "admitted" {
			t.Fatal("private handoff metadata missing", err)
		}
		if strings.Contains(w.Body.String(), "one@example.net") || strings.Contains(w.Body.String(), "Your Gregale account") || strings.Contains(w.Body.String(), "recipient_address") {
			t.Fatal("private inspection included payload")
		}
	}
	// A second lost acknowledgement exhausts producer retries. The private
	// receipt and apid owner operation settle it without another admission.
	toReconcile := schedule(t, f, account(t, f))
	reconcileID := uuidString(toReconcile.ID.Bytes)
	// A fresh transport makes the deliberately lost response observable by
	// the producer instead of an HTTP reconnect replay on an idle connection.
	restarted, err = NewFromEnv(pool, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	dropped.Store(false)
	if err := restarted.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, reconcileID); got.State != "queued" {
		t.Fatal("test failed to lose the second admission response", got.State)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET attempts=32 WHERE id=$1", reconcileID); err != nil {
		t.Fatal(err)
	}
	if err := restarted.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, reconcileID); got.State != "failed" {
		t.Fatal("lost acknowledgement did not reach retained failure", got.State, got.Attempts, got.Code)
	}
	admissionPath := internalmail.ProducerOutboxPath + "/items/" + reconcileID + "/admission"
	r := httptest.NewRequest(http.MethodGet, admissionPath, nil)
	r.Header.Set("Authorization", "Bearer "+operatorToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatal("default-off admission lookup was visible")
	}
	reconciliationGate.Store(true)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	var review internalmail.ProducerOutboxAdmissionReview
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &review) != nil || !review.CanAcknowledge || review.AdmissionStatus != "confirmed" {
		t.Fatal("private original admission lookup failed", w.Code)
	}
	for _, value := range []string{"one@example.net", "notify@example.test", "account-delete:", "Your Gregale account", "sealed_payload", "payload_fingerprint"} {
		if strings.Contains(w.Body.String(), value) {
			t.Fatal("receipt lookup exposed payload or replay input", value)
		}
	}
	input := reconciliationInput(row(t, f, reconcileID), review.Receipt.MessageID)
	if _, err := reconciler(t, f).Acknowledge(t.Context(), reconcileID, input); err != nil {
		t.Fatal("owner failed to acknowledge real maild receipt", err)
	}
	if err := restarted.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := daemon.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if smtp.accepted.Load() != 2 {
		t.Fatal("reconciliation changed SMTP custody or duplicated handoff")
	}
	// Verification has a separate remote gate. Persist its encrypted credential,
	// refuse it while the maild template gate is off, then lose an accepted
	// response and restart the producer using the same original link and key.
	f.service = restarted
	configureVerification(t, f)
	verification := queueVerification(t, f, account(t, f))
	verificationID := uuidString(verification.ID.Bytes)
	if err := restarted.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, verificationID); got.State != "queued" || got.Code != "maild_unavailable" || len(got.SealedVerification) == 0 {
		t.Fatal("remote verification gate discarded the original credential")
	}
	verificationGate.Store(true)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", verificationID); err != nil {
			t.Fatal(err)
		}
		restarted, err = NewFromEnv(pool, func(k string) string { return env[k] })
		if err != nil {
			t.Fatal(err)
		}
		if err := restarted.ConfigureVerification(t.Context(), func(k string) string { return env[k] }); err != nil {
			t.Fatal(err)
		}
		if attempt == 0 {
			dropped.Store(false)
		}
		if err := restarted.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := row(t, f, verificationID); attempt == 0 && got.State != "queued" {
			t.Fatal("verification admission response was not lost")
		}
	}
	if got := row(t, f, verificationID); got.State != "admitted" {
		t.Fatal("verification restart did not acknowledge its original admission")
	} else {
		assertVerificationErased(t, got)
	}
	for range 2 {
		if err := daemon.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if smtp.accepted.Load() != 3 {
		t.Fatal("verification replay duplicated its SMTP handoff")
	}
	// Reset admission has its own remote gate, independently of verification.
	// Lose one committed acknowledgement and replay the same reset after restart.
	env[internalmail.PasswordResetOutboxEnableEnv] = "1"
	env[PasswordResetOriginEnv] = "https://dashboard.example.test"
	f.service = restarted
	if err := restarted.ConfigurePasswordReset(t.Context(), func(k string) string { return env[k] }); err != nil {
		t.Fatal(err)
	}
	reset := queueReset(t, f, resetAccount(t, f))
	resetID := uuidString(reset.ID.Bytes)
	if err := restarted.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, resetID); got.State != "queued" || len(got.SealedVerification) == 0 {
		t.Fatal("remote reset gate discarded its credential")
	}
	passwordResetGate.Store(true)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", resetID); err != nil {
			t.Fatal(err)
		}
		restarted, err = NewFromEnv(pool, func(k string) string { return env[k] })
		if err != nil {
			t.Fatal(err)
		}
		if err := restarted.ConfigureVerification(t.Context(), func(k string) string { return env[k] }); err != nil {
			t.Fatal(err)
		}
		if err := restarted.ConfigurePasswordReset(t.Context(), func(k string) string { return env[k] }); err != nil {
			t.Fatal(err)
		}
		if attempt == 0 {
			dropped.Store(false)
		}
		if err := restarted.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := row(t, f, resetID); attempt == 0 && got.State != "queued" {
			t.Fatal("reset admission acknowledgement was not lost")
		}
	}
	if got := row(t, f, resetID); got.State != "admitted" {
		t.Fatal("reset replay lost original admission")
	} else {
		assertVerificationErased(t, got)
	}
	for range 2 {
		if err := daemon.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if smtp.accepted.Load() != 4 {
		t.Fatal("reset replay duplicated SMTP custody")
	}
	// Magic-link admission has its own remote gate, independently of verification.
	// Lose one committed acknowledgement and replay the same magic-link after restart.
	env[internalmail.MagicLinkOutboxEnableEnv] = "1"
	env[MagicLinkOriginEnv] = "https://dashboard.example.test"
	f.service = restarted
	if err := restarted.ConfigureMagicLink(t.Context(), func(k string) string { return env[k] }); err != nil {
		t.Fatal(err)
	}
	magic := queueMagic(t, f, magicAccount(t, f))
	magicID := uuidString(magic.ID.Bytes)
	if err := restarted.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, magicID); got.State != "queued" || len(got.SealedVerification) == 0 {
		t.Fatal("remote magic-link gate discarded its credential")
	}
	magicLinkGate.Store(true)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", magicID); err != nil {
			t.Fatal(err)
		}
		restarted, err = NewFromEnv(pool, func(k string) string { return env[k] })
		if err != nil {
			t.Fatal(err)
		}
		if err := restarted.ConfigureVerification(t.Context(), func(k string) string { return env[k] }); err != nil {
			t.Fatal(err)
		}
		if err := restarted.ConfigureMagicLink(t.Context(), func(k string) string { return env[k] }); err != nil {
			t.Fatal(err)
		}
		if attempt == 0 {
			dropped.Store(false)
		}
		if err := restarted.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := row(t, f, magicID); attempt == 0 && got.State != "queued" {
			t.Fatal("magic-link admission acknowledgement was not lost")
		}
	}
	if got := row(t, f, magicID); got.State != "admitted" {
		t.Fatal("magic-link replay lost original admission")
	} else {
		assertVerificationErased(t, got)
	}
	for range 2 {
		if err := daemon.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if smtp.accepted.Load() != 5 {
		t.Fatal("magic-link replay duplicated SMTP custody")
	}
	// MFA recovery alerts have an independent remote gate. Lose one committed
	// acknowledgement and replay the same event after restart.
	env[internalmail.MFARecoveryAlertOutboxEnableEnv] = "1"
	f.service = restarted
	if err := restarted.ConfigureMFARecoveryAlert(t.Context(), func(k string) string { return env[k] }); err != nil {
		t.Fatal(err)
	}
	accountID, hashes := mfaAlertAccount(t, f, 3)
	alert := queueMFAAlert(t, f, accountID, hashes[0])
	alertID := uuidString(alert.ID.Bytes)
	if err := restarted.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, alertID); got.State != "queued" || len(got.SealedSecurityAlert) == 0 {
		t.Fatal("remote MFA recovery alert gate discarded its event")
	}
	mfaAlertGate.Store(true)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", alertID); err != nil {
			t.Fatal(err)
		}
		restarted, err = NewFromEnv(pool, func(k string) string { return env[k] })
		if err != nil {
			t.Fatal(err)
		}
		if err := restarted.ConfigureVerification(t.Context(), func(k string) string { return env[k] }); err != nil {
			t.Fatal(err)
		}
		if err := restarted.ConfigureMFARecoveryAlert(t.Context(), func(k string) string { return env[k] }); err != nil {
			t.Fatal(err)
		}
		if attempt == 0 {
			dropped.Store(false)
		}
		if err := restarted.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := row(t, f, alertID); attempt == 0 && got.State != "queued" {
			t.Fatal("MFA recovery alert admission acknowledgement was not lost")
		}
	}
	if got := row(t, f, alertID); got.State != "admitted" {
		t.Fatal("MFA recovery alert replay lost original admission")
	} else {
		assertMFAAlertErased(t, got)
	}
	for range 2 {
		if err := daemon.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if smtp.accepted.Load() != 6 {
		t.Fatal("MFA recovery alert replay duplicated SMTP custody")
	}
	// Disable-email links retain their token and original cooldown through an
	// independent remote pause and a lost committed admission acknowledgement.
	env[internalmail.MFADisableOutboxEnableEnv] = "1"
	env[MFADisableOriginEnv] = "https://dashboard.example.test"
	f.service = restarted
	if err := restarted.ConfigureMFADisable(t.Context(), func(k string) string { return env[k] }); err != nil {
		t.Fatal(err)
	}
	disable := queueDisable(t, f, disableAccount(t, f))
	savedToken := disableToken(t, f, disable)
	disableID := uuidString(disable.ID.Bytes)
	if err := restarted.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, disableID); got.State != "queued" || len(got.SealedVerification) == 0 {
		t.Fatal("remote disable gate discarded its credential")
	}
	mfaDisableGate.Store(true)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", disableID); err != nil {
			t.Fatal(err)
		}
		restarted, err = NewFromEnv(pool, func(k string) string { return env[k] })
		if err != nil {
			t.Fatal(err)
		}
		if err := restarted.ConfigureVerification(t.Context(), func(k string) string { return env[k] }); err != nil {
			t.Fatal(err)
		}
		if err := restarted.ConfigureMFADisable(t.Context(), func(k string) string { return env[k] }); err != nil {
			t.Fatal(err)
		}
		if attempt == 0 {
			dropped.Store(false)
		}
		if err := restarted.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := row(t, f, disableID); attempt == 0 && got.State != "queued" {
			t.Fatal("disable-email admission acknowledgement was not lost")
		}
	}
	if got := row(t, f, disableID); got.State != "admitted" {
		t.Fatal("disable-email retry lost original admission")
	} else {
		assertMFAAlertErased(t, got)
	}
	if got := disableToken(t, f, disable); !reflect.DeepEqual(got, savedToken) {
		t.Fatal("admission retries reset cooldown or token expiry")
	}
	for range 2 {
		if err := daemon.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if smtp.accepted.Load() != 7 {
		t.Fatal("disable-email retry duplicated SMTP custody")
	}
	// A committed removal alert survives a remote pause and a lost response.
	env[internalmail.MFADisabledAlertOutboxEnableEnv] = "1"
	f.service = restarted
	if err := restarted.ConfigureMFADisabledAlert(t.Context(), func(k string) string { return env[k] }); err != nil {
		t.Fatal(err)
	}
	removedID, removedHashes := mfaAlertAccount(t, f, 3)
	removed := queueDisabledRecovery(t, f, removedID, removedHashes[0])
	if err := restarted.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(removed.ID.Bytes)); got.State != "queued" || len(got.SealedSecurityAlert) == 0 {
		t.Fatal("remote alert rollback discarded committed history")
	}
	mfaDisabledAlertGate.Store(true)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", removed.ID); err != nil {
			t.Fatal(err)
		}
		restarted, err = NewFromEnv(pool, func(k string) string { return env[k] })
		if err != nil {
			t.Fatal(err)
		}
		if err := restarted.ConfigureMFADisabledAlert(t.Context(), func(k string) string { return env[k] }); err != nil {
			t.Fatal(err)
		}
		if attempt == 0 {
			dropped.Store(false)
		}
		if err := restarted.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := row(t, f, uuidString(removed.ID.Bytes)); attempt == 0 && got.State != "queued" {
			t.Fatal("alert admission acknowledgement was not lost")
		}
	}
	if got := row(t, f, uuidString(removed.ID.Bytes)); got.State != "admitted" {
		t.Fatal("alert retry lost receipt")
	} else {
		assertMFAAlertErased(t, got)
	}
	for range 2 {
		if err := daemon.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if smtp.accepted.Load() != 8 {
		t.Fatal("MFA disabled alert retry duplicated SMTP custody")
	}
	// A committed password change alert survives a remote pause and a lost response.
	env[internalmail.PasswordChangedAlertOutboxEnableEnv] = "1"
	f.service = restarted
	if err := restarted.ConfigurePasswordChangedAlert(t.Context(), func(k string) string { return env[k] }); err != nil {
		t.Fatal(err)
	}
	changedID := account(t, f)
	changed := queueChanged(t, f, changedID)
	if err := restarted.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(changed.ID.Bytes)); got.State != "queued" || len(got.SealedSecurityAlert) == 0 {
		t.Fatal("remote alert rollback discarded committed history")
	}
	passwordChangedAlertGate.Store(true)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", changed.ID); err != nil {
			t.Fatal(err)
		}
		restarted, err = NewFromEnv(pool, func(k string) string { return env[k] })
		if err != nil {
			t.Fatal(err)
		}
		if err := restarted.ConfigurePasswordChangedAlert(t.Context(), func(k string) string { return env[k] }); err != nil {
			t.Fatal(err)
		}
		if attempt == 0 {
			dropped.Store(false)
		}
		if err := restarted.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := row(t, f, uuidString(changed.ID.Bytes)); attempt == 0 && got.State != "queued" {
			t.Fatal("alert admission acknowledgement was not lost")
		}
	}
	if got := row(t, f, uuidString(changed.ID.Bytes)); got.State != "admitted" {
		t.Fatal("alert retry lost receipt")
	} else {
		assertMFAAlertErased(t, got)
	}
	for range 2 {
		if err := daemon.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if smtp.accepted.Load() != 9 {
		t.Fatal("password changed alert retry duplicated SMTP custody")
	}
	// A committed MFA activation alert survives a remote pause and a lost response.
	env[internalmail.MFAEnabledAlertOutboxEnableEnv] = "1"
	f.service = restarted
	if err := restarted.ConfigureMFAEnabledAlert(t.Context(), func(k string) string { return env[k] }); err != nil {
		t.Fatal(err)
	}
	enabledID := account(t, f)
	enabled := queueEnabled(t, f, enabledID)
	if err := restarted.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(enabled.ID.Bytes)); got.State != "queued" || len(got.SealedSecurityAlert) == 0 {
		t.Fatal("remote alert rollback discarded committed history")
	}
	mfaEnabledAlertGate.Store(true)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", enabled.ID); err != nil {
			t.Fatal(err)
		}
		restarted, err = NewFromEnv(pool, func(k string) string { return env[k] })
		if err != nil {
			t.Fatal(err)
		}
		if err := restarted.ConfigureMFAEnabledAlert(t.Context(), func(k string) string { return env[k] }); err != nil {
			t.Fatal(err)
		}
		if attempt == 0 {
			dropped.Store(false)
		}
		if err := restarted.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := row(t, f, uuidString(enabled.ID.Bytes)); attempt == 0 && got.State != "queued" {
			t.Fatal("alert admission acknowledgement was not lost")
		}
	}
	if got := row(t, f, uuidString(enabled.ID.Bytes)); got.State != "admitted" {
		t.Fatal("alert retry lost receipt")
	} else {
		assertMFAAlertErased(t, got)
	}
	for range 2 {
		if err := daemon.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if smtp.accepted.Load() != 10 {
		t.Fatal("MFA enabled alert retry duplicated SMTP custody")
	}
	f.service = restarted
	expectedMailCount := 10 + exercisePaymentHandoff(t, f, daemon, smtp, &paymentFailedGate, &dropped)
	expectedMailCount += exerciseRestoredHandoff(t, f, daemon, smtp, &accountRestoredGate, &dropped)
	expectedMailCount += exerciseSuspendedHandoff(t, f, daemon, smtp, &accountSuspendedGate, &dropped)
	expectedMailCount += exerciseNonpaymentDeletionHandoff(t, f, daemon, smtp, &nonpaymentDeletionGate, &dropped)
	expectedMailCount += exerciseQuotaWarningHandoff(t, f, daemon, smtp, &quotaWarningGate, &dropped)
	expectedMailCount += exerciseSubscriptionEndedHandoff(t, f, daemon, smtp, &subscriptionEndedGate, &dropped)
	expectedMailCount += exerciseDeploymentFailedHandoff(t, f, daemon, smtp, &deploymentFailedGate, &dropped)
	// The producer must retain no recovery advisory lock across HTTP. Start
	// and release a new recovery after producer preflight but before maild
	// admission: the owner's generation check must reject this stale request.
	late := schedule(t, f, account(t, f))
	recoverBeforeAdmission.Store(true)
	if err := restarted.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-recoveryDone:
		if err != nil {
			t.Fatal("producer held recovery fence across HTTP", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("recovery did not reach the handoff boundary")
	}
	if got := row(t, f, uuid.UUID(late.ID.Bytes).String()); got.State != "queued" {
		t.Fatal("stale generation was admitted")
	}
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM internal_mail_messages WHERE producer_id='auth'").Scan(&count); err != nil || count != expectedMailCount {
		t.Fatal("stale generation created new mail after release", err)
	}
	// A remote management rollback keeps a generation-bound intent retryable.
	paused := schedule(t, f, account(t, f))
	outboxGate.Store(false)
	if err := restarted.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuid.UUID(paused.ID.Bytes).String()); got.State != "queued" || got.Code != "maild_unavailable" {
		t.Fatal("remote gate rollback discarded pending intent")
	}
}

// Uses the real Unix handler, owner transactions and fake SMTP. No external
// transport or billing API is contacted.
func exercisePaymentHandoff(t *testing.T, f *fixture, daemon *internalmail.Service, smtp *fixtureSMTP, gate, dropped *atomic.Bool) int {
	t.Helper()
	f.env[internalmail.PaymentFailedOutboxEnableEnv] = "1"
	if err := f.service.ConfigurePaymentFailed(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	_, _, first := queuePayment(t, f)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(first.ID.Bytes)); got.State != "queued" || len(got.SealedSecurityAlert) == 0 {
		t.Fatal("remote pause lost payment warning")
	}
	gate.Store(true)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", first.ID); err != nil {
			t.Fatal(err)
		}
		restarted, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
		if err != nil {
			t.Fatal(err)
		}
		if err := restarted.ConfigurePaymentFailed(t.Context(), func(k string) string { return f.env[k] }); err != nil {
			t.Fatal(err)
		}
		f.service = restarted
		if attempt == 0 {
			dropped.Store(false)
		}
		if err := restarted.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := row(t, f, uuidString(first.ID.Bytes)); attempt == 0 && got.State != "queued" {
			t.Fatal("payment admission response was not lost")
		}
	}
	if got := row(t, f, uuidString(first.ID.Bytes)); got.State != "admitted" {
		t.Fatal("payment retry lost admission")
	} else {
		assertMFAAlertErased(t, got)
	}
	before := smtp.accepted.Load()
	gate.Store(false)
	if err := daemon.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if smtp.accepted.Load() != before {
		t.Fatal("disabled payment delivery reached SMTP")
	}
	gate.Store(true)
	for range 2 {
		if err := daemon.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if smtp.accepted.Load() != before+1 {
		t.Fatal("lost acknowledgement duplicated payment SMTP custody")
	}
	admitted := 1
	for _, mode := range []string{"paid", "new_episode", "address", "erasure", "recovery_wait", "flag_wait"} {
		id, customer, intent := queuePayment(t, f)
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		if row(t, f, uuidString(intent.ID.Bytes)).State != "admitted" {
			t.Fatal("payment warning not admitted", mode)
		}
		admitted++
		var err error
		switch mode {
		case "paid":
			_, err = f.service.RestorePaymentAfterFailure(t.Context(), id, "stripe", customer)
		case "new_episode":
			_, err = f.service.RestorePaymentAfterFailure(t.Context(), id, "stripe", customer)
			if err == nil {
				err = f.service.RecordPaymentFailure(t.Context(), id, "stripe", uuid.NewString(), customer, billing.EventPaymentFailed)
			}
		case "address":
			_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET email='other@example.net' WHERE id=$1", id)
		case "erasure":
			_, err = f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", id)
		case "recovery_wait", "flag_wait":
			tx, beginErr := f.pool.Begin(t.Context())
			if beginErr != nil {
				t.Fatal(beginErr)
			}
			defer rollback(t.Context(), tx)
			if _, err := tx.Exec(t.Context(), "SELECT id FROM accounts WHERE id=$1 FOR UPDATE", id); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- daemon.DeliverOne(ctx) }()
			waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
			if mode == "recovery_wait" {
				if _, err := tx.Exec(ctx, "UPDATE accounts SET status='active',past_due_at=NULL WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
			} else {
				gate.Store(false)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if mode == "flag_wait" {
				gate.Store(true)
				_, err = f.service.RestorePaymentAfterFailure(t.Context(), id, "stripe", customer)
			}
		}
		if err != nil {
			t.Fatal(mode, err)
		}
		for range 2 {
			if err := daemon.DeliverOne(t.Context()); err != nil {
				t.Fatal(mode, err)
			}
		}
		if smtp.accepted.Load() != before+1 {
			t.Fatal("obsolete payment warning reached SMTP", mode)
		}
		var state string
		if err := f.pool.QueryRow(t.Context(), "SELECT r.status FROM internal_mail_messages m JOIN internal_mail_recipients r ON r.message_id=m.id WHERE m.idempotency_key=$1", "payment-failed:"+uuidString(intent.ID.Bytes)).Scan(&state); err != nil || state != "cancelled" {
			t.Fatal("maild did not cancel obsolete payment warning", mode, state, err)
		}
		if mode == "new_episode" {
			if _, err := f.service.RestorePaymentAfterFailure(t.Context(), id, "stripe", customer); err != nil {
				t.Fatal(err)
			}
		}
	}
	f.env[internalmail.PaymentFailedOutboxEnableEnv] = "0"
	return admitted
}

func exerciseRestoredHandoff(t *testing.T, f *fixture, daemon *internalmail.Service, smtp *fixtureSMTP, gate, dropped *atomic.Bool) int {
	t.Helper()
	f.env[internalmail.AccountRestoredOutboxEnableEnv] = "1"
	if err := f.service.ConfigureAccountRestored(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	_, _, first := queueRestored(t, f)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(first.ID.Bytes)); got.State != "queued" || len(got.SealedSecurityAlert) == 0 {
		t.Fatal("remote pause lost committed recovery")
	}
	gate.Store(true)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", first.ID); err != nil {
			t.Fatal(err)
		}
		next, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
		if err != nil {
			t.Fatal(err)
		}
		if err := next.ConfigureAccountRestored(t.Context(), func(k string) string { return f.env[k] }); err != nil {
			t.Fatal(err)
		}
		f.service = next
		if attempt == 0 {
			dropped.Store(false)
		}
		if err := next.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := row(t, f, uuidString(first.ID.Bytes)); attempt == 0 && got.State != "queued" {
			t.Fatal("recovery admission acknowledgement was not lost")
		}
	}
	if got := row(t, f, uuidString(first.ID.Bytes)); got.State != "admitted" {
		t.Fatal("recovery retry lost admission")
	} else {
		assertMFAAlertErased(t, got)
	}
	before := smtp.accepted.Load()
	gate.Store(false)
	if err := daemon.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if smtp.accepted.Load() != before {
		t.Fatal("disabled recovery template reached SMTP")
	}
	gate.Store(true)
	for range 2 {
		if err := daemon.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if smtp.accepted.Load() != before+1 {
		t.Fatal("recovery retry duplicated SMTP custody")
	}
	store := state.NewPgStore(f.pool)
	admitted := 1
	for _, mode := range []string{"status_aba", "address_aba", "deleted", "erasure", "status_wait", "flag_wait"} {
		id, _, intent := queueRestored(t, f)
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		if row(t, f, uuidString(intent.ID.Bytes)).State != "admitted" {
			t.Fatal("recovery confirmation not admitted", mode)
		}
		admitted++
		var err error
		switch mode {
		case "status_aba":
			err = store.MarkDunningStep(t.Context(), id, state.AccountActive, state.AccountPastDue)
			if err == nil {
				err = store.UpdateAccountStatus(t.Context(), id, state.AccountActive)
			}
		case "address_aba":
			_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET email='other@example.net' WHERE id=$1", id)
			if err == nil {
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET email='one@example.net' WHERE id=$1", id)
			}
		case "deleted":
			_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET status='deleted_pending',deletion_requested_at=clock_timestamp() WHERE id=$1", id)
		case "erasure":
			_, err = f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", id)
		case "status_wait", "flag_wait":
			tx, beginErr := f.pool.Begin(t.Context())
			if beginErr != nil {
				t.Fatal(beginErr)
			}
			defer rollback(t.Context(), tx)
			if _, err := tx.Exec(t.Context(), "SELECT id FROM accounts WHERE id=$1 FOR UPDATE", id); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- daemon.DeliverOne(ctx) }()
			waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
			if mode == "status_wait" {
				if _, err := tx.Exec(ctx, "UPDATE accounts SET status='past_due',past_due_at=clock_timestamp() WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
			} else {
				gate.Store(false)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if mode == "flag_wait" {
				var status string
				if err := f.pool.QueryRow(ctx, "SELECT r.status FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.id=r.message_id WHERE m.idempotency_key=$1", "account-restored:"+uuidString(intent.ID.Bytes)).Scan(&status); err != nil || status != "queued" {
					t.Fatal("late gate withdrawal did not hold confirmation", status, err)
				}
				gate.Store(true)
				err = store.UpdateAccountStatus(t.Context(), id, state.AccountSuspended)
			}
		}
		if err != nil {
			t.Fatal(mode, err)
		}
		for range 2 {
			if err := daemon.DeliverOne(t.Context()); err != nil {
				t.Fatal(mode, err)
			}
		}
		if smtp.accepted.Load() != before+1 {
			t.Fatal("stale recovery reached SMTP", mode)
		}
		var status string
		if err := f.pool.QueryRow(t.Context(), "SELECT r.status FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.id=r.message_id WHERE m.idempotency_key=$1", "account-restored:"+uuidString(intent.ID.Bytes)).Scan(&status); err != nil || status != "cancelled" {
			t.Fatal("maild did not cancel stale recovery", mode, status, err)
		}
	}
	f.env[internalmail.AccountRestoredOutboxEnableEnv] = "0"
	return admitted
}

func exerciseSuspendedHandoff(t *testing.T, f *fixture, daemon *internalmail.Service, smtp *fixtureSMTP, gate, dropped *atomic.Bool) int {
	t.Helper()
	if err := daemon.Store.(*internalmail.PGStore).AccountSuspendedOutboxReady(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.env[internalmail.AccountSuspendedOutboxEnableEnv] = "1"
	if err := f.service.ConfigureAccountSuspended(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	_, _, first, _ := queueSuspended(t, f)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(first.ID.Bytes)); got.State != "queued" || len(got.SealedSecurityAlert) == 0 {
		t.Fatal("remote pause lost committed suspension")
	}
	gate.Store(true)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", first.ID); err != nil {
			t.Fatal(err)
		}
		next, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
		if err != nil {
			t.Fatal(err)
		}
		if err := next.ConfigureAccountSuspended(t.Context(), func(k string) string { return f.env[k] }); err != nil {
			t.Fatal(err)
		}
		f.service = next
		if attempt == 0 {
			dropped.Store(false)
		}
		if err := next.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := row(t, f, uuidString(first.ID.Bytes)); attempt == 0 && got.State != "queued" {
			t.Fatal("suspension admission acknowledgement was not lost")
		}
	}
	if got := row(t, f, uuidString(first.ID.Bytes)); got.State != "admitted" {
		t.Fatal("suspension retry lost admission")
	} else {
		assertMFAAlertErased(t, got)
	}
	before := smtp.accepted.Load()
	gate.Store(false)
	if err := daemon.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if smtp.accepted.Load() != before {
		t.Fatal("disabled suspension template reached SMTP")
	}
	gate.Store(true)
	for range 2 {
		if err := daemon.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if smtp.accepted.Load() != before+1 {
		t.Fatal("suspension retry duplicated SMTP custody")
	}
	store := state.NewPgStore(f.pool)
	admitted := 1
	for _, mode := range []string{"status_aba", "address_aba", "deleted", "erasure", "status_wait", "flag_wait"} {
		id, _, intent, _ := queueSuspended(t, f)
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		if row(t, f, uuidString(intent.ID.Bytes)).State != "admitted" {
			t.Fatal("suspension notice not admitted", mode)
		}
		admitted++
		var err error
		switch mode {
		case "status_aba":
			err = store.MarkDunningStep(t.Context(), id, state.AccountSuspended, state.AccountPastDue)
			if err == nil {
				err = store.MarkDunningStep(t.Context(), id, state.AccountPastDue, state.AccountSuspended)
			}
		case "address_aba":
			_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET email='other@example.net' WHERE id=$1", id)
			if err == nil {
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET email='one@example.net' WHERE id=$1", id)
			}
		case "deleted":
			_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET status='deleted_pending',deletion_requested_at=clock_timestamp() WHERE id=$1", id)
		case "erasure":
			_, err = f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", id)
		case "status_wait", "flag_wait":
			tx, beginErr := f.pool.Begin(t.Context())
			if beginErr != nil {
				t.Fatal(beginErr)
			}
			defer rollback(t.Context(), tx)
			if _, err := tx.Exec(t.Context(), "SELECT id FROM accounts WHERE id=$1 FOR UPDATE", id); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- daemon.DeliverOne(ctx) }()
			waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
			if mode == "status_wait" {
				if _, err := tx.Exec(ctx, "UPDATE accounts SET status='active',past_due_at=NULL WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
			} else {
				gate.Store(false)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if mode == "flag_wait" {
				var status string
				if err := f.pool.QueryRow(ctx, "SELECT r.status FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.id=r.message_id WHERE m.idempotency_key=$1", "account-suspended:"+uuidString(intent.ID.Bytes)).Scan(&status); err != nil || status != "queued" {
					t.Fatal("late gate withdrawal did not hold notice", status, err)
				}
				gate.Store(true)
				err = store.UpdateAccountStatus(t.Context(), id, state.AccountActive)
			}
		}
		if err != nil {
			t.Fatal(mode, err)
		}
		for range 2 {
			if err := daemon.DeliverOne(t.Context()); err != nil {
				t.Fatal(mode, err)
			}
		}
		if smtp.accepted.Load() != before+1 {
			t.Fatal("stale suspension reached SMTP", mode)
		}
		var status string
		if err := f.pool.QueryRow(t.Context(), "SELECT r.status FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.id=r.message_id WHERE m.idempotency_key=$1", "account-suspended:"+uuidString(intent.ID.Bytes)).Scan(&status); err != nil || status != "cancelled" {
			t.Fatal("maild did not cancel stale suspension", mode, status, err)
		}
	}
	f.env[internalmail.AccountSuspendedOutboxEnableEnv] = "0"
	return admitted
}

func exerciseNonpaymentDeletionHandoff(t *testing.T, f *fixture, daemon *internalmail.Service, smtp *fixtureSMTP, gate, dropped *atomic.Bool) int {
	t.Helper()
	if err := daemon.Store.(*internalmail.PGStore).NonpaymentDeletionOutboxReady(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.env[internalmail.NonpaymentDeletionOutboxEnableEnv] = "1"
	if err := f.service.ConfigureNonpaymentDeletion(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	_, _, first, _ := queueNonpaymentDeletion(t, f)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(first.ID.Bytes)); got.State != "queued" || len(got.SealedSecurityAlert) == 0 {
		t.Fatal("remote pause lost committed non-payment deletion")
	}
	gate.Store(true)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", first.ID); err != nil {
			t.Fatal(err)
		}
		next, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
		if err != nil {
			t.Fatal(err)
		}
		if err := next.ConfigureNonpaymentDeletion(t.Context(), func(k string) string { return f.env[k] }); err != nil {
			t.Fatal(err)
		}
		f.service = next
		if attempt == 0 {
			dropped.Store(false)
		}
		if err := next.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := row(t, f, uuidString(first.ID.Bytes)); attempt == 0 && got.State != "queued" {
			t.Fatal("non-payment deletion admission acknowledgement was not lost")
		}
	}
	if got := row(t, f, uuidString(first.ID.Bytes)); got.State != "admitted" {
		t.Fatal("non-payment deletion retry lost admission")
	} else {
		assertMFAAlertErased(t, got)
	}
	before := smtp.accepted.Load()
	gate.Store(false)
	if err := daemon.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if smtp.accepted.Load() != before {
		t.Fatal("disabled non-payment deletion template reached SMTP")
	}
	gate.Store(true)
	for range 2 {
		if err := daemon.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if smtp.accepted.Load() != before+1 {
		t.Fatal("non-payment deletion retry duplicated SMTP custody")
	}
	store := state.NewPgStore(f.pool)
	admitted := 1
	for _, mode := range []string{"recovered", "status_aba", "address_aba", "deadline_changed", "erasure", "status_wait", "flag_wait"} {
		id, _, intent, _ := queueNonpaymentDeletion(t, f)
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		if row(t, f, uuidString(intent.ID.Bytes)).State != "admitted" {
			t.Fatal("non-payment deletion notice not admitted", mode)
		}
		admitted++
		var err error
		switch mode {
		case "recovered":
			err = store.UpdateAccountStatus(t.Context(), id, state.AccountActive)
		case "status_aba":
			err = store.MarkDunningStep(t.Context(), id, state.AccountDeletedPending, state.AccountSuspended)
			if err == nil {
				err = store.MarkDunningStep(t.Context(), id, state.AccountSuspended, state.AccountDeletedPending)
			}
		case "address_aba":
			_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET email='other@example.net' WHERE id=$1", id)
			if err == nil {
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET email='one@example.net' WHERE id=$1", id)
			}
		case "deadline_changed":
			_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET deletion_requested_at=deletion_requested_at+interval '1 hour' WHERE id=$1", id)
		case "erasure":
			_, err = f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", id)
		case "status_wait", "flag_wait":
			tx, beginErr := f.pool.Begin(t.Context())
			if beginErr != nil {
				t.Fatal(beginErr)
			}
			defer rollback(t.Context(), tx)
			if _, err := tx.Exec(t.Context(), "SELECT id FROM accounts WHERE id=$1 FOR UPDATE", id); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- daemon.DeliverOne(ctx) }()
			waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
			if mode == "status_wait" {
				if _, err := tx.Exec(ctx, "UPDATE accounts SET status='active',past_due_at=NULL WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
			} else {
				gate.Store(false)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if mode == "flag_wait" {
				var status string
				if err := f.pool.QueryRow(ctx, "SELECT r.status FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.id=r.message_id WHERE m.idempotency_key=$1", "nonpayment-deletion:"+uuidString(intent.ID.Bytes)).Scan(&status); err != nil || status != "queued" {
					t.Fatal("late gate withdrawal did not hold notice", status, err)
				}
				gate.Store(true)
				err = store.UpdateAccountStatus(t.Context(), id, state.AccountActive)
			}
		}
		if err != nil {
			t.Fatal(mode, err)
		}
		for range 2 {
			if err := daemon.DeliverOne(t.Context()); err != nil {
				t.Fatal(mode, err)
			}
		}
		if smtp.accepted.Load() != before+1 {
			t.Fatal("stale non-payment deletion reached SMTP", mode)
		}
		var status string
		if err := f.pool.QueryRow(t.Context(), "SELECT r.status FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.id=r.message_id WHERE m.idempotency_key=$1", "nonpayment-deletion:"+uuidString(intent.ID.Bytes)).Scan(&status); err != nil || status != "cancelled" {
			t.Fatal("maild did not cancel stale non-payment deletion", mode, status, err)
		}
	}
	f.env[internalmail.NonpaymentDeletionOutboxEnableEnv] = "0"
	return admitted
}

func exerciseQuotaWarningHandoff(t *testing.T, f *fixture, daemon *internalmail.Service, smtp *fixtureSMTP, gate, dropped *atomic.Bool) int {
	t.Helper()
	if err := daemon.Store.(*internalmail.PGStore).QuotaWarningOutboxReady(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.env[internalmail.QuotaWarningOutboxEnableEnv] = "1"
	if err := f.service.ConfigureQuotaWarning(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	_, _, first, _ := queueQuotaWarning(t, f)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(first.ID.Bytes)); got.State != "queued" || len(got.SealedSecurityAlert) == 0 {
		t.Fatal("remote pause lost committed quota warning")
	}
	gate.Store(true)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", first.ID); err != nil {
			t.Fatal(err)
		}
		next, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
		if err != nil {
			t.Fatal(err)
		}
		if err := next.ConfigureQuotaWarning(t.Context(), func(k string) string { return f.env[k] }); err != nil {
			t.Fatal(err)
		}
		f.service = next
		if attempt == 0 {
			dropped.Store(false)
		}
		if err := next.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := row(t, f, uuidString(first.ID.Bytes)); attempt == 0 && got.State != "queued" {
			t.Fatal("quota warning admission acknowledgement was not lost")
		}
	}
	if got := row(t, f, uuidString(first.ID.Bytes)); got.State != "admitted" {
		t.Fatal("quota warning retry lost admission")
	} else {
		assertMFAAlertErased(t, got)
	}
	before := smtp.accepted.Load()
	gate.Store(false)
	if err := daemon.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if smtp.accepted.Load() != before {
		t.Fatal("disabled quota warning template reached SMTP")
	}
	gate.Store(true)
	for range 2 {
		if err := daemon.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if smtp.accepted.Load() != before+1 {
		t.Fatal("quota warning retry duplicated SMTP custody")
	}
	admitted := 1
	for _, mode := range []string{"plan_aba", "status_aba", "address_aba", "stamp_aba", "deleting", "day_rollover", "erasure", "plan_wait", "flag_wait"} {
		id, _, intent, _ := queueQuotaWarning(t, f)
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		if row(t, f, uuidString(intent.ID.Bytes)).State != "admitted" {
			t.Fatal("quota warning notice not admitted", mode)
		}
		admitted++
		var err error
		switch mode {
		case "plan_aba":
			_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET plan='scale' WHERE id=$1", id)
			if err == nil {
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET plan='pro' WHERE id=$1", id)
			}
		case "status_aba":
			_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET status='suspended' WHERE id=$1", id)
			if err == nil {
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET status='active' WHERE id=$1", id)
			}
		case "address_aba":
			_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET email='other@example.net' WHERE id=$1", id)
			if err == nil {
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET email='one@example.net' WHERE id=$1", id)
			}
		case "stamp_aba":
			_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET last_quota_warning_at=NULL WHERE id=$1", id)
			if err == nil {
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET last_quota_warning_at=$2 WHERE id=$1", id, intent.QuotaWarningDay.Time)
			}
		case "deleting":
			_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET deletion_requested_at=clock_timestamp() WHERE id=$1", id)
		case "day_rollover":
			// Keep expiry in the future to exercise maild's own UTC-day guard.
			_, err = f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET quota_warning_day=quota_warning_day-interval '1 day' WHERE id=$1", intent.ID)
		case "erasure":
			_, err = f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", id)
		case "plan_wait", "flag_wait":
			tx, beginErr := f.pool.Begin(t.Context())
			if beginErr != nil {
				t.Fatal(beginErr)
			}
			defer rollback(t.Context(), tx)
			if _, err := tx.Exec(t.Context(), "SELECT id FROM accounts WHERE id=$1 FOR UPDATE", id); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- daemon.DeliverOne(ctx) }()
			waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
			if mode == "plan_wait" {
				if _, err := tx.Exec(ctx, "UPDATE accounts SET plan='scale' WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
			} else {
				gate.Store(false)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if mode == "flag_wait" {
				var status string
				if err := f.pool.QueryRow(ctx, "SELECT r.status FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.id=r.message_id WHERE m.idempotency_key=$1", "quota-warning:"+uuidString(intent.ID.Bytes)).Scan(&status); err != nil || status != "queued" {
					t.Fatal("late gate withdrawal did not hold notice", status, err)
				}
				gate.Store(true)
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET plan='scale' WHERE id=$1", id)
			}
		}
		if err != nil {
			t.Fatal(mode, err)
		}
		for range 2 {
			if err := daemon.DeliverOne(t.Context()); err != nil {
				t.Fatal(mode, err)
			}
		}
		if smtp.accepted.Load() != before+1 {
			t.Fatal("stale quota warning reached SMTP", mode)
		}
		var status string
		if err := f.pool.QueryRow(t.Context(), "SELECT r.status FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.id=r.message_id WHERE m.idempotency_key=$1", "quota-warning:"+uuidString(intent.ID.Bytes)).Scan(&status); err != nil || status != "cancelled" {
			t.Fatal("maild did not cancel stale quota warning", mode, status, err)
		}
	}
	f.env[internalmail.QuotaWarningOutboxEnableEnv] = "0"
	return admitted
}

func exerciseSubscriptionEndedHandoff(t *testing.T, f *fixture, daemon *internalmail.Service, smtp *fixtureSMTP, gate, dropped *atomic.Bool) int {
	t.Helper()
	if err := daemon.Store.(*internalmail.PGStore).SubscriptionEndedOutboxReady(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.env[internalmail.SubscriptionEndedOutboxEnableEnv] = "1"
	if err := f.service.ConfigureSubscriptionEnded(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	_, _, _, first := queueEnded(t, f)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(first.ID.Bytes)); got.State != "queued" || len(got.SealedSecurityAlert) == 0 {
		t.Fatal("remote pause lost committed subscription-ended confirmation")
	}
	gate.Store(true)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", first.ID); err != nil {
			t.Fatal(err)
		}
		next, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
		if err != nil {
			t.Fatal(err)
		}
		if err := next.ConfigureSubscriptionEnded(t.Context(), func(k string) string { return f.env[k] }); err != nil {
			t.Fatal(err)
		}
		f.service = next
		if attempt == 0 {
			dropped.Store(false)
		}
		if err := next.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := row(t, f, uuidString(first.ID.Bytes)); attempt == 0 && got.State != "queued" {
			t.Fatal("subscription-ended confirmation admission acknowledgement was not lost")
		}
	}
	if got := row(t, f, uuidString(first.ID.Bytes)); got.State != "admitted" {
		t.Fatal("subscription-ended confirmation retry lost admission")
	} else {
		assertMFAAlertErased(t, got)
	}
	before := smtp.accepted.Load()
	gate.Store(false)
	if err := daemon.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if smtp.accepted.Load() != before {
		t.Fatal("disabled subscription-ended confirmation template reached SMTP")
	}
	gate.Store(true)
	for range 2 {
		if err := daemon.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if smtp.accepted.Load() != before+1 {
		t.Fatal("subscription-ended confirmation retry duplicated SMTP custody")
	}
	admitted := 1
	for _, mode := range []string{"plan_aba", "status_aba", "address_aba", "identity_aba", "identity_reinsert", "other_provider", "deleting", "erasure", "plan_wait", "identity_wait", "flag_wait"} {
		id, customer, _, intent := queueEnded(t, f)
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		if row(t, f, uuidString(intent.ID.Bytes)).State != "admitted" {
			t.Fatal("subscription-ended confirmation notice not admitted", mode)
		}
		admitted++
		var err error
		switch mode {
		case "plan_aba":
			_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET plan='scale' WHERE id=$1", id)
			if err == nil {
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET plan='free' WHERE id=$1", id)
			}
		case "status_aba":
			_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET status='suspended' WHERE id=$1", id)
			if err == nil {
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET status='active' WHERE id=$1", id)
			}
		case "address_aba":
			_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET email='other@example.net' WHERE id=$1", id)
			if err == nil {
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET email='one@example.net' WHERE id=$1", id)
			}
		case "identity_aba":
			_, err = f.pool.Exec(t.Context(), "UPDATE billing_identities SET subscription_id='new-sub' WHERE account_id=$1", id)
			if err == nil {
				_, err = f.pool.Exec(t.Context(), "UPDATE billing_identities SET subscription_id='' WHERE account_id=$1", id)
			}
		case "identity_reinsert":
			_, err = f.pool.Exec(t.Context(), "DELETE FROM billing_identities WHERE account_id=$1", id)
			if err == nil {
				_, err = f.pool.Exec(t.Context(), "INSERT INTO billing_identities(account_id,provider,customer_id) VALUES($1,'stripe',$2)", id, customer)
			}
		case "other_provider":
			_, err = f.pool.Exec(t.Context(), "INSERT INTO billing_identities(account_id,provider,customer_id,subscription_id) VALUES($1,'polar',$2,'new-sub')", id, "new-"+customer)
		case "deleting":
			_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET deletion_requested_at=clock_timestamp() WHERE id=$1", id)
		case "erasure":
			_, err = f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", id)
		case "plan_wait", "identity_wait", "flag_wait":
			tx, beginErr := f.pool.Begin(t.Context())
			if beginErr != nil {
				t.Fatal(beginErr)
			}
			defer rollback(t.Context(), tx)
			if _, err := tx.Exec(t.Context(), "SELECT id FROM accounts WHERE id=$1 FOR UPDATE", id); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- daemon.DeliverOne(ctx) }()
			waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
			if mode == "plan_wait" {
				if _, err := tx.Exec(ctx, "UPDATE accounts SET plan='scale' WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
			} else if mode == "identity_wait" {
				if _, err := tx.Exec(ctx, "UPDATE billing_identities SET subscription_id='new-sub' WHERE account_id=$1", id); err != nil {
					t.Fatal(err)
				}
			} else {
				gate.Store(false)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if mode == "flag_wait" {
				var status string
				if err := f.pool.QueryRow(ctx, "SELECT r.status FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.id=r.message_id WHERE m.idempotency_key=$1", "subscription-ended:"+uuidString(intent.ID.Bytes)).Scan(&status); err != nil || status != "queued" {
					t.Fatal("late gate withdrawal did not hold notice", status, err)
				}
				gate.Store(true)
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET plan='scale' WHERE id=$1", id)
			}
		}
		if err != nil {
			t.Fatal(mode, err)
		}
		for range 2 {
			if err := daemon.DeliverOne(t.Context()); err != nil {
				t.Fatal(mode, err)
			}
		}
		if smtp.accepted.Load() != before+1 {
			t.Fatal("stale subscription-ended confirmation reached SMTP", mode)
		}
		var status string
		if err := f.pool.QueryRow(t.Context(), "SELECT r.status FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.id=r.message_id WHERE m.idempotency_key=$1", "subscription-ended:"+uuidString(intent.ID.Bytes)).Scan(&status); err != nil || status != "cancelled" {
			t.Fatal("maild did not cancel stale subscription-ended confirmation", mode, status, err)
		}
	}
	f.env[internalmail.SubscriptionEndedOutboxEnableEnv] = "0"
	return admitted
}
