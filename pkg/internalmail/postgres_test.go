package internalmail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/emersion/go-msgauth/dkim"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

type recordingTransport struct {
	mu          sync.Mutex
	envelopes   []Envelope
	state, code string
}

func (r *recordingTransport) Submit(_ context.Context, e Envelope) (string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.envelopes = append(r.envelopes, e)
	if r.state != "" {
		return r.state, r.code
	}
	return "submitted", "relay_accepted"
}

type pgFixture struct {
	service           *Service
	store             *PGStore
	pool              *pgxpool.Pool
	resolver          testResolver
	transport         *recordingTransport
	enabled, delivery *atomic.Bool
	domain            DomainResponse
}

func fixture(t *testing.T) *pgFixture {
	t.Helper()
	pool := pgtest.Open(t)
	var updates []string
	for _, name := range []string{"20261015121450648_internal_mail_service.sql", "20261015151448279_internal_mail_payload_retention.sql", "20261015154854536_internal_mail_operations_audit.sql", "20261015173147723_internal_mail_domain_lifecycle.sql", "20261016085426393_internal_mail_delivery_controls.sql", "20261016095545574_internal_mail_producers.sql", "20261016103033444_internal_mail_scheduling.sql", "20261016110548882_internal_mail_domain_pacing.sql", "20261016112746813_internal_mail_relay_backpressure.sql", "20261016120104359_internal_mail_reputation.sql", "20261016123727973_internal_mail_event_feed.sql", "20261016130417046_internal_mail_producer_scheduler.sql", "20261016152534774_internal_mail_producer_lifecycle.sql", "20261016170332382_internal_mail_encryption_rotation.sql", "20261016174046483_internal_mail_suppression_controls.sql", "20261016181202093_internal_mail_recovery.sql"} {
		raw, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		updates = append(updates, strings.Split(strings.Split(string(raw), "-- +goose StatementBegin\n")[1], "-- +goose StatementEnd")[0])
	}
	projection, err := os.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(projection), strings.Join(updates, "\n")) {
		t.Fatal("sqlc projection differs from migration")
	}
	for range 2 {
		for _, up := range updates {
			if _, err := pool.Exec(t.Context(), up); err != nil {
				t.Fatal(err)
			}
		}
	}
	store, err := NewPGStore(pool, "platform")
	if err != nil {
		t.Fatal(err)
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	enabled, delivery := &atomic.Bool{}, &atomic.Bool{}
	enabled.Store(true)
	resolver := testResolver{}
	transport := &recordingTransport{}
	s := &Service{Store: store, Codec: AgeCodec{Identity: id, Namespace: "platform"}, Resolver: resolver, SendingIP: netip.MustParseAddr("192.0.2.10"), Enabled: enabled.Load, DeliveryEnabled: delivery.Load, Transport: transport, Ready: store.Ready, RecipientAllowed: func(string) bool { return true }}
	domain, err := s.RegisterDomain(t.Context(), "example.test")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range domain.Records {
		resolver[record.Name] = []string{record.Value}
	}
	domain, err = s.VerifyDomain(t.Context(), domain.ID)
	if err != nil {
		t.Fatal(err)
	}
	return &pgFixture{s, store, pool, resolver, transport, enabled, delivery, domain}
}
func request() SendRequest {
	return SendRequest{From: "Gregale <notify@example.test>", To: []string{"one@example.net", "two@example.net"}, Subject: "Account verification", Text: "Secret verification code 123456", HTML: "<p>Secret verification code 123456</p>"}
}
func submit(t *testing.T, f *pgFixture, key string, r SendRequest) MessageResponse {
	t.Helper()
	m, err := f.service.Submit(t.Context(), key, r)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPGInternalMailDeliveryAndDKIM(t *testing.T) {
	f := fixture(t)
	m := submit(t, f, "verification", request())
	if bytes.Contains(m.SealedPayload, []byte("123456")) {
		t.Fatal("unencrypted message")
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("sealed")) || bytes.Contains(raw, []byte("123456")) {
		t.Fatal("secret in API representation")
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.transport.envelopes) != 0 {
		t.Fatal("delivery while flag off")
	}
	before, err := f.store.Message(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Recipients[0].Attempts != 0 {
		t.Fatal("disabled worker claimed mail")
	}
	f.delivery.Store(true)
	for range 2 {
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range f.transport.envelopes {
		checks, err := dkim.VerifyWithOptions(bytes.NewReader(e.Data), &dkim.VerifyOptions{LookupTXT: func(name string) ([]string, error) { return f.resolver.LookupTXT(t.Context(), name) }})
		if err != nil || len(checks) != 1 || checks[0].Err != nil {
			t.Fatalf("DKIM: %v %v", checks, err)
		}
		parsed, err := mail.ReadMessage(bytes.NewReader(e.Data))
		if err != nil {
			t.Fatal(err)
		}
		if parsed.Header.Get("To") != e.To {
			t.Fatal("recipient disclosure")
		}
		if !strings.HasPrefix(e.From, "bounce+") || !strings.HasSuffix(e.From, "@example.test") {
			t.Fatal("missing correlated bounce sender")
		}
	}
	after, err := f.store.Message(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range after.Recipients {
		if r.Status != "submitted" {
			t.Fatal(r.Status)
		}
	}
	r := after.Recipients[0]
	event := Report{EventID: "relay-acceptance", RecipientID: r.ID, Kind: "accepted", Code: "smtp_250"}
	for range 2 {
		if err := f.store.Report(t.Context(), event); err != nil {
			t.Fatal(err)
		}
	}
	event.Code = "changed"
	if err := f.store.Report(t.Context(), event); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}

func TestPGConcurrentIdempotencyAndNamespaceIsolation(t *testing.T) {
	f := fixture(t)
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			m, err := f.service.Submit(t.Context(), "same", request())
			if err != nil {
				errs <- err
				return
			}
			ids <- m.ID
		})
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatal("duplicate accepted message")
		}
	}
	changed := request()
	changed.Text = "different"
	if _, err := f.service.Submit(t.Context(), "same", changed); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	other, err := NewPGStore(f.pool, "other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Message(t.Context(), first); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := other.Domain(t.Context(), f.domain.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestPGLeaseRecoveryDoesNotResubmitUncertainMail(t *testing.T) {
	f := fixture(t)
	m := submit(t, f, "one", request())
	old, err := f.store.Claim(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recipients SET lease_until=now()-interval '1 second' WHERE id=$1", old.ID); err != nil {
		t.Fatal(err)
	}
	current, err := f.store.Claim(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if current.ID != old.ID || current.LeaseID == old.LeaseID {
		t.Fatal("claim was not fenced")
	}
	if err := f.store.BeginSubmission(t.Context(), old); !errors.Is(err, ErrLeaseLost) {
		t.Fatal(err)
	}
	if err := f.store.BeginSubmission(t.Context(), current); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recipients SET lease_until=now()-interval '1 second' WHERE id=$1", current.ID); err != nil {
		t.Fatal(err)
	}
	// The second recipient remains claimable, but the ambiguous one cannot be.
	other, err := f.store.Claim(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if other.ID == current.ID {
		t.Fatal("resubmitted uncertain mail")
	}
	if err := f.store.Settle(t.Context(), old, "submitted", "old_worker", time.Now()); !errors.Is(err, ErrLeaseLost) {
		t.Fatal(err)
	}
	state, err := f.store.Message(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range state.Recipients {
		if r.ID == current.ID {
			found = r.Status == "uncertain"
		}
	}
	if !found {
		t.Fatal("lost uncertainty state")
	}
	if err := f.store.Report(t.Context(), Report{EventID: "operator-reconciled", RecipientID: current.ID, Kind: "retry", Code: "confirmed_not_accepted"}); err != nil {
		t.Fatal(err)
	}
}

func TestPGBouncesSuppressOnlyTheirNamespaceAndRelayDeferralsDoNotResend(t *testing.T) {
	f := fixture(t)
	m := submit(t, f, "first", request())
	f.delivery.Store(true)
	for range 2 {
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	after, err := f.store.Message(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	recipient := after.Recipients[0]
	if err := f.store.Report(t.Context(), Report{EventID: "deferred", RecipientID: recipient.ID, Kind: "deferred", Code: "smtp_450"}); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.transport.envelopes) != 2 {
		t.Fatal("relay-owned retry was resubmitted")
	}
	if err := f.store.Report(t.Context(), Report{EventID: "bounce", RecipientID: recipient.ID, Kind: "bounced", Code: "smtp_550"}); err != nil {
		t.Fatal(err)
	}
	r := request()
	r.To = []string{recipient.Address}
	next := submit(t, f, "second", r)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	state, err := f.store.Message(t.Context(), next.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Recipients[0].Status != "suppressed" || len(f.transport.envelopes) != 2 {
		t.Fatal("bounced recipient was sent again")
	}
	other, err := NewPGStore(f.pool, "other")
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := other.Suppressed(t.Context(), recipient.Address)
	if err != nil || blocked {
		t.Fatal("cross-namespace suppression", err)
	}
}

func TestPGInternalSafeguardsAndDomainDisable(t *testing.T) {
	f := fixture(t)
	r := request()
	r.To = []string{"one@example.net"}
	m := submit(t, f, "disabled-domain", r)
	if err := f.store.SetDomainStatus(t.Context(), f.domain.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := f.store.Message(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Recipients[0].Status != "failed" || len(f.transport.envelopes) != 0 {
		t.Fatal("disabled domain sent mail")
	}
	if _, err := f.service.VerifyDomain(t.Context(), f.domain.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("reactivated disabled domain", err)
	}
	if err := f.store.SetDomainStatus(t.Context(), f.domain.ID, "verified"); err != nil {
		t.Fatal(err)
	}
	r.To = nil
	for range api.InternalMailMaxRecipients {
		r.To = append(r.To, uuid.NewString()+"@example.net")
	}
	for i := 1; i < api.InternalMailDailyRecipients/api.InternalMailMaxRecipients; i++ {
		submit(t, f, uuid.NewString(), r)
	}
	r.To = r.To[:api.InternalMailMaxRecipients-1]
	submit(t, f, "fill-quota", r)
	r.To = r.To[:1]
	if _, err := f.service.Submit(t.Context(), "over-quota", r); !errors.Is(err, ErrQuota) {
		t.Fatal("missing daily quota", err)
	}
}

func TestPGDomainProofCannotSkipDNSAndQueuedMailExpires(t *testing.T) {
	f := fixture(t)
	d, err := f.service.RegisterDomain(t.Context(), "unverified.test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.VerifyDomain(t.Context(), d.ID); !errors.Is(err, ErrUnverified) {
		t.Fatal(err)
	}
	r := request()
	r.From = "notify@unverified.test"
	if _, err := f.service.Submit(t.Context(), "unverified", r); !errors.Is(err, ErrUnverified) {
		t.Fatal(err)
	}
	m := submit(t, f, "expired", request())
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_messages SET created_at=now()-interval '2 days' WHERE id=$1", m.ID); err != nil {
		t.Fatal(err)
	}
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	state, err := f.store.Message(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range state.Recipients {
		if r.Status != "failed" {
			t.Fatal(r.Status)
		}
	}
}

func TestPGPrivateHTTPMessageLifecycle(t *testing.T) {
	f := fixture(t)
	handler, err := Handler(f.service, testToken)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(request())
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path string, payload []byte) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewReader(payload))
		r.Header.Set("Authorization", "Bearer "+testToken)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "http-message")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	first := call(http.MethodPost, APIPath+"/messages", body)
	if first.Code != http.StatusAccepted {
		t.Fatalf("submit: %d %s", first.Code, first.Body.String())
	}
	var accepted MessageResponse
	if err := json.Unmarshal(first.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	again := call(http.MethodPost, APIPath+"/messages", body)
	var replay MessageResponse
	if err := json.Unmarshal(again.Body.Bytes(), &replay); err != nil {
		t.Fatal(err)
	}
	if again.Code != http.StatusAccepted || accepted.ID != replay.ID {
		t.Fatal("non-idempotent HTTP retry")
	}
	status := call(http.MethodGet, APIPath+"/messages/"+accepted.ID, nil)
	if status.Code != http.StatusOK || bytes.Contains(status.Body.Bytes(), []byte("123456")) {
		t.Fatal("status leaked a message body")
	}
	events := call(http.MethodGet, APIPath+"/messages/"+accepted.ID+"/events", nil)
	if events.Code != http.StatusOK || !bytes.Contains(events.Body.Bytes(), []byte("queued")) {
		t.Fatal("missing committed event")
	}
	if ready := call(http.MethodGet, "/readyz", nil); ready.Code != http.StatusOK {
		t.Fatal("schema not ready")
	}
	f.enabled.Store(false)
	if disabled := call(http.MethodGet, APIPath+"/messages/"+accepted.ID, nil); disabled.Code != http.StatusNotFound {
		t.Fatal("read route survived disable")
	}
}

type disableAtSubmission struct {
	Store
	disable func()
}

func (s disableAtSubmission) BeginSubmission(ctx context.Context, r Recipient) error {
	if err := s.Store.BeginSubmission(ctx, r); err != nil {
		return err
	}
	s.disable()
	return nil
}

func TestPGDeliveryFlagIsRecheckedAtSubmission(t *testing.T) {
	f := fixture(t)
	m := submit(t, f, "kill-switch", request())
	f.delivery.Store(true)
	f.service.Store = disableAtSubmission{Store: f.store, disable: func() { f.delivery.Store(false) }}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.transport.envelopes) != 0 {
		t.Fatal("SMTP after delivery kill switch")
	}
	state, err := f.store.Message(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range state.Recipients {
		if r.Status != "queued" {
			t.Fatal("disabled work not returned to queue", r.Status)
		}
	}
}
