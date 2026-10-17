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
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/internalmail"
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
	if _, err := pool.Exec(t.Context(), `CREATE TABLE accounts(id uuid PRIMARY KEY,email text NOT NULL,status text NOT NULL,deletion_requested_at timestamptz,email_verified_at timestamptz,abuse_hold_at timestamptz,mfa_enrolled_at timestamptz,mfa_recovery_codes_hash bytea[],mfa_secret_encrypted bytea,mfa_required boolean NOT NULL DEFAULT false);
CREATE TABLE mfa_disable_requests(token_hash bytea PRIMARY KEY,account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,requested_at timestamptz NOT NULL,consumed_at timestamptz);
CREATE TABLE email_verification_tokens(token_hash bytea PRIMARY KEY,account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,expires_at timestamptz NOT NULL,consumed_at timestamptz,created_at timestamptz NOT NULL DEFAULT now());
        CREATE TABLE account_passwords(account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,hash text NOT NULL,updated_at timestamptz NOT NULL DEFAULT now());
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
	outboxGate.Store(true)
	daemon := &internalmail.Service{Store: store, Codec: internalmail.AgeCodec{Identity: identity, Namespace: "platform"}, Resolver: resolver, SendingIP: netip.MustParseAddr("192.0.2.10"),
		Enabled: func() bool { return true }, ProducersEnabled: func() bool { return true }, SchedulingEnabled: func() bool { return true }, DeliveryEnabled: func() bool { return true },
		ProducerOutboxEnabled: outboxGate.Load, OperationsEnabled: func() bool { return true }, OperationsToken: operatorToken,
		OutboxReconciliationEnabled:   reconciliationGate.Load,
		VerificationOutboxEnabled:     verificationGate.Load,
		PasswordResetOutboxEnabled:    passwordResetGate.Load,
		MagicLinkOutboxEnabled:        magicLinkGate.Load,
		MFARecoveryAlertOutboxEnabled: mfaAlertGate.Load,
		MFADisableOutboxEnabled:       mfaDisableGate.Load,
		MFADisabledAlertOutboxEnabled: mfaDisabledAlertGate.Load,
		Producers:                     map[string]internalmail.Producer{"auth": {ID: "auth", Token: testProducerToken, From: []string{"notify@example.test"}, DailyRecipientLimit: 10, PendingRecipientLimit: 100}},
		RecipientAllowed:              func(string) bool { return true }, Transport: smtp}
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
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM internal_mail_messages WHERE producer_id='auth'").Scan(&count); err != nil || count != 8 {
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
