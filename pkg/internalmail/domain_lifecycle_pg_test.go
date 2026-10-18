package internalmail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-msgauth/dkim"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

func domainFixture(t *testing.T) *pgFixture {
	t.Helper()
	f := operationsFixture(t)
	f.service.RotationEnabled = func() bool { return true }
	f.service.ReverificationEnabled = func() bool { return true }
	if _, err := f.service.ReverifyDomain(t.Context(), f.domain.ID, auditInput()); err != nil {
		t.Fatal(err)
	}
	return f
}
func stageKey(t *testing.T, f *pgFixture) DKIMKey {
	t.Helper()
	record, err := f.service.StageDKIMKey(t.Context(), f.domain.ID, auditInput())
	if err != nil {
		t.Fatal(err)
	}
	var key DKIMKey
	if json.Unmarshal(record.Result, &key) != nil || key.ID == "" || key.Record.Value == "" {
		t.Fatal("stage omitted DNS instructions")
	}
	return key
}
func verifiedKey(t *testing.T, f *pgFixture) DKIMKey {
	t.Helper()
	key := stageKey(t, f)
	f.resolver[key.Record.Name] = []string{key.Record.Value}
	if _, err := f.service.VerifyDKIMKey(t.Context(), f.domain.ID, key.ID, auditInput()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_dkim_keys SET first_verified_at=now()-interval '2 hours' WHERE namespace='platform' AND id=$1", key.ID); err != nil {
		t.Fatal(err)
	}
	return key
}
func assertSignedWith(t *testing.T, f *pgFixture, raw []byte, selector string) {
	t.Helper()
	verifications, err := dkim.VerifyWithOptions(bytes.NewReader(raw), &dkim.VerifyOptions{LookupTXT: func(name string) ([]string, error) { return f.resolver.LookupTXT(t.Context(), name) }})
	if err != nil || len(verifications) != 1 || verifications[0].Err != nil || !bytes.Contains(raw, []byte("s="+selector)) {
		t.Fatal("DKIM signing continuity failed", err, verifications)
	}
}

func TestPGDKIMStagingPropagationActivationAndReplay(t *testing.T) {
	f := domainFixture(t)
	input := auditInput()
	staged, err := f.service.StageDKIMKey(t.Context(), f.domain.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	var key DKIMKey
	if json.Unmarshal(staged.Result, &key) != nil {
		t.Fatal("invalid key result")
	}
	before, _ := f.store.Domain(t.Context(), f.domain.ID)
	if before.Selector != f.domain.Selector || before.DKIMKeyID != "" {
		t.Fatal("staging changed the active signer")
	}
	replay, err := f.service.StageDKIMKey(t.Context(), f.domain.ID, input)
	if err != nil || replay.ID != staged.ID || !replay.replayed {
		t.Fatal("stage replay created another key", err)
	}
	if _, err := f.service.StageDKIMKey(t.Context(), f.domain.ID, auditInput()); !errors.Is(err, ErrConflict) {
		t.Fatal("two simultaneous staged keys allowed", err)
	}
	if _, err := f.service.ActivateDKIMKey(t.Context(), f.domain.ID, key.ID, auditInput()); !errors.Is(err, ErrKeyPropagation) {
		t.Fatal("unpublished key activated", err)
	}
	failed, err := f.service.VerifyDKIMKey(t.Context(), f.domain.ID, key.ID, auditInput())
	if err != nil || !bytes.Contains(failed.Result, []byte("unavailable")) {
		t.Fatal("failed publication observation was not auditable", err)
	}
	stats, err := f.store.DomainLifecycleStatistics(t.Context(), f.service.SendingIP.String())
	if err != nil || stats.FailedKeys != 1 {
		t.Fatal("rotation failure missing durable monitoring", err)
	}
	f.resolver[key.Record.Name] = []string{key.Record.Value}
	verified, err := f.service.VerifyDKIMKey(t.Context(), f.domain.ID, key.ID, auditInput())
	if err != nil {
		t.Fatal(err)
	}
	var checked DKIMKey
	if json.Unmarshal(verified.Result, &checked) != nil || checked.ActivateAfter == nil {
		t.Fatal("verification omitted propagation deadline")
	}
	if _, err := f.service.ActivateDKIMKey(t.Context(), f.domain.ID, key.ID, auditInput()); !errors.Is(err, ErrKeyPropagation) {
		t.Fatal("propagation wait bypassed", err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_dkim_keys SET first_verified_at=now()-interval '2 hours' WHERE id=$1", key.ID); err != nil {
		t.Fatal(err)
	}
	activation := auditInput()
	record, err := f.service.ActivateDKIMKey(t.Context(), f.domain.ID, key.ID, activation)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := f.store.Domain(t.Context(), f.domain.ID)
	if after.Selector != key.Selector || after.DKIMKeyID != key.ID || after.AuthorizationState != "valid" {
		t.Fatal("activation did not atomically update signer and proof")
	}
	restarted, _ := NewPGStore(f.pool, "platform")
	f.service.Store = restarted
	if replay, err := f.service.ActivateDKIMKey(t.Context(), f.domain.ID, key.ID, activation); err != nil || replay.ID != record.ID {
		t.Fatal("activation replay failed after restart", err)
	}
	activation.Evidence = "ticket:changed"
	if _, err := f.service.ActivateDKIMKey(t.Context(), f.domain.ID, key.ID, activation); !errors.Is(err, ErrConflict) {
		t.Fatal("contradictory activation replay accepted", err)
	}
	// The ciphertext is bound to the key ID, not only to the domain.
	row, err := f.store.queries.DKIMKey(t.Context(), f.pool, q.DKIMKeyParams{Namespace: "platform", DomainID: pgID(f.domain.ID), ID: pgID(key.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Codec.Open(dkimKeyResource(f.domain.ID, uuid.NewString()), row.SealedKey); err == nil {
		t.Fatal("key ciphertext moved to a different key ID")
	}
	inputMessage := request()
	inputMessage.To = []string{"one@example.net"}
	m := submit(t, f, "new-signer", inputMessage)
	raw, err := f.service.render(m.Message, m.Recipients[0], after)
	if err != nil {
		t.Fatal(err)
	}
	assertSignedWith(t, f, raw, key.Selector)
	page, err := f.store.operatorDKIMKeys(t.Context(), f.domain.ID, OperatorList{Limit: 1})
	if err != nil || page.Next == "" {
		t.Fatal("key history is not paginated", err)
	}
	for _, secret := range []string{"sealed_key", "PRIVATE KEY", "verification_token"} {
		if strings.Contains(string(record.Result), secret) {
			t.Fatal("rotation leaked signing material")
		}
	}
}

func TestPGDKIMSubmissionFenceOverlapAndOldSignature(t *testing.T) {
	f := domainFixture(t)
	m := submit(t, f, "old-signer", request())
	old, _ := f.store.Domain(t.Context(), f.domain.ID)
	r, err := f.store.claimAuthorized(t.Context(), f.service.authorizationRequirement())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := f.service.render(m.Message, r, old)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.beginSignedSubmission(t.Context(), r, old.Selector, f.service.authorizationRequirement()); err != nil {
		t.Fatal(err)
	}
	key := verifiedKey(t, f)
	if _, err := f.service.ActivateDKIMKey(t.Context(), f.domain.ID, key.ID, auditInput()); err != nil {
		t.Fatal(err)
	}
	assertSignedWith(t, f, raw, old.Selector)
	if err := f.store.Settle(t.Context(), r, "submitted", "relay_accepted", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.RetireDKIMKey(t.Context(), f.domain.ID, f.domain.ID, auditInput(), true); !errors.Is(err, ErrKeyOverlap) {
		t.Fatal("old selector retired before overlap", err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_dkim_keys SET deactivated_at=now()-interval '8 days' WHERE id=$1", f.domain.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.RetireDKIMKey(t.Context(), f.domain.ID, f.domain.ID, auditInput(), true); !errors.Is(err, ErrKeyOverlap) {
		t.Fatal("relay-owned mail lost selector", err)
	}
	claimed, err := f.store.claimAuthorized(t.Context(), f.service.authorizationRequirement())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.beginSignedSubmission(t.Context(), claimed, old.Selector, f.service.authorizationRequirement()); !errors.Is(err, ErrUnverified) {
		t.Fatal("stale rendered signer submitted", err)
	}
	if err := f.store.Settle(t.Context(), claimed, "failed", "synthetic_done", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recipients SET signing_selector='' WHERE id=$1", r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.RetireDKIMKey(t.Context(), f.domain.ID, f.domain.ID, auditInput(), true); !errors.Is(err, ErrKeyOverlap) {
		t.Fatal("unknown legacy relay custody ignored", err)
	}
	if err := f.store.Report(t.Context(), Report{EventID: uuid.NewString(), RecipientID: r.ID, Kind: "accepted"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.RetireDKIMKey(t.Context(), f.domain.ID, f.domain.ID, auditInput(), false); !errors.Is(err, ErrInvalid) {
		t.Fatal("retirement omitted explicit queue review", err)
	}
	before, _ := f.store.Domain(t.Context(), f.domain.ID)
	if _, err := f.service.RetireDKIMKey(t.Context(), f.domain.ID, f.domain.ID, auditInput(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.recordDomainObservation(t.Context(), before, "mismatch", f.service.SendingIP.String(), func() bool { return true }); !errors.Is(err, ErrConflict) {
		t.Fatal("pre-retirement DNS observation changed authorization", err)
	}
	row, err := f.store.queries.DKIMKey(t.Context(), f.pool, q.DKIMKeyParams{Namespace: "platform", DomainID: pgID(f.domain.ID), ID: pgID(f.domain.ID)})
	if err != nil || row.State != "retired" || len(row.SealedKey) != 0 || row.PublicKey == "" {
		t.Fatal("retirement lost public history or retained private key", err)
	}
}

func TestPGDomainReverificationPausesAdmissionAndPreservesQueuedAttempts(t *testing.T) {
	f := domainFixture(t)
	m := submit(t, f, "paused-queue", request())
	original := f.service.Resolver
	f.service.Resolver = resolverFunc(func(ctx context.Context, name string) ([]string, error) {
		if name == "_dmarc."+f.domain.Name {
			return nil, &net.DNSError{IsTimeout: true}
		}
		return original.LookupTXT(ctx, name)
	})
	if _, err := f.service.ReverifyDomain(t.Context(), f.domain.ID, auditInput()); err != nil {
		t.Fatal(err)
	}
	d, _ := f.store.Domain(t.Context(), f.domain.ID)
	if d.FailureCount != 0 || d.AuthorizationState != "valid" || d.CheckResult != "unavailable" {
		t.Fatal("transient DNS caused a confirmed failure")
	}
	f.service.Resolver = resolverFunc(func(ctx context.Context, name string) ([]string, error) {
		if name == "_dmarc."+f.domain.Name {
			return nil, &net.DNSError{IsNotFound: true}
		}
		return original.LookupTXT(ctx, name)
	})
	for range 2 {
		if _, err := f.service.ReverifyDomain(t.Context(), f.domain.ID, auditInput()); err != nil {
			t.Fatal(err)
		}
	}
	d, _ = f.store.Domain(t.Context(), f.domain.ID)
	if d.FailureCount != 1 || d.AuthorizationState != "failing" {
		t.Fatal("rapid manual checks confirmed one outage twice")
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_domains SET first_failure_at=now()-interval '6 minutes',last_checked_at=now()-interval '6 minutes' WHERE id=$1", d.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ReverifyDomains(t.Context()); err != nil {
		t.Fatal(err)
	}
	d, _ = f.store.Domain(t.Context(), f.domain.ID)
	if d.AuthorizationState != "paused" || d.FailureCount != 2 {
		t.Fatal("worker did not confirm pause")
	}
	if _, err := f.service.Submit(t.Context(), "new-while-paused", request()); !errors.Is(err, ErrUnverified) {
		t.Fatal("paused domain admitted new mail", err)
	}
	f.delivery.Store(true)
	for range 3 {
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := f.store.Message(t.Context(), m.ID)
	for _, r := range after.Recipients {
		if r.Attempts != 0 || r.Status != "queued" {
			t.Fatal("authorization pause consumed queued attempts")
		}
	}
	f.service.ReverificationEnabled = func() bool { return false }
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.transport.envelopes) != 0 {
		t.Fatal("disabling checks bypassed pause")
	}
	f.service.ReverificationEnabled = func() bool { return true }
	f.service.Resolver = original
	if _, err := f.service.ReverifyDomain(t.Context(), f.domain.ID, auditInput()); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.transport.envelopes) != 1 {
		t.Fatal("fresh valid proof did not resume queued mail")
	}
	if err := f.store.SetDomainStatus(t.Context(), f.domain.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ReverifyDomain(t.Context(), f.domain.ID, auditInput()); !errors.Is(err, ErrConflict) {
		t.Fatal("reverification re-enabled disabled domain", err)
	}
}

func TestPGDomainFreshnessIPScopeAndStaleObservations(t *testing.T) {
	f := operationsFixture(t)
	f.service.ReverificationEnabled = func() bool { return true }
	if _, err := f.service.Submit(t.Context(), "unchecked", request()); !errors.Is(err, ErrUnverified) {
		t.Fatal("unchecked domain admitted mail", err)
	}
	if err := f.service.ReverifyDomains(t.Context()); err != nil {
		t.Fatal(err)
	}
	m := submit(t, f, "fresh-proof", request())
	snapshot, _ := f.store.Domain(t.Context(), f.domain.ID)
	if _, err := f.service.ReverifyDomain(t.Context(), f.domain.ID, auditInput()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.recordDomainObservation(t.Context(), snapshot, "mismatch", f.service.SendingIP.String(), func() bool { return true }); !errors.Is(err, ErrConflict) {
		t.Fatal("stale check regressed fresh proof", err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_domains SET last_verified_at=now()-interval '25 hours' WHERE id=$1", f.domain.ID); err != nil {
		t.Fatal(err)
	}
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.transport.envelopes) != 0 {
		t.Fatal("stale authorization reached SMTP")
	}
	if _, err := f.service.Submit(t.Context(), "stale-proof", request()); !errors.Is(err, ErrUnverified) {
		t.Fatal("stale authorization admitted mail", err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_domains SET last_verified_at=now(),verified_sending_ip='192.0.2.11' WHERE id=$1", f.domain.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.transport.envelopes) != 0 {
		t.Fatal("previous sending IP authorized new source")
	}
	other, _ := NewPGStore(f.pool, "other")
	f.service.Store = other
	if _, err := f.service.ReverifyDomain(t.Context(), f.domain.ID, auditInput()); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-namespace domain check", err)
	}
	f.service.Store = f.store
	// Idempotent reads remain available while fresh admission is blocked.
	if replay, err := f.service.Submit(t.Context(), "fresh-proof", request()); err != nil || replay.ID != m.ID {
		t.Fatal("authorization pause broke idempotency", err)
	}
}

func TestPGDomainLifecycleAuditsAndGateRevocationRollback(t *testing.T) {
	f := domainFixture(t)
	h, err := Handler(f.service, testToken)
	if err != nil {
		t.Fatal(err)
	}
	input := auditInput()
	raw, _ := json.Marshal(input)
	r := httptest.NewRequest(http.MethodPost, OperationsPath+"/domains/"+f.domain.ID+"/keys/stage", bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+operatorToken)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), input.RequestID) || strings.Contains(w.Body.String(), "sealed_key") {
		t.Fatal("HTTP audit body was not committed", w.Code, w.Body.String())
	}
	var record OperationRecord
	if json.Unmarshal(w.Body.Bytes(), &record) != nil {
		t.Fatal("invalid audit")
	}
	var key DKIMKey
	if json.Unmarshal(record.Result, &key) != nil {
		t.Fatal("invalid stage")
	}
	if _, err := f.service.RetireDKIMKey(t.Context(), f.domain.ID, key.ID, auditInput(), true); err != nil {
		t.Fatal("unused staged key could not be discarded", err)
	}
	var enabled atomic.Bool
	enabled.Store(true)
	_, err = f.store.stageDKIM(t.Context(), f.domain.ID, auditInput(), func() (DKIMKey, []byte, error) {
		key, sealed, err := f.service.newDKIMKey(f.domain.ID)
		enabled.Store(false)
		return key, sealed, err
	}, enabled.Load)
	if !errors.Is(err, ErrDisabled) {
		t.Fatal("revoked rotation committed", err)
	}
	keys, _ := f.store.operatorDKIMKeys(t.Context(), f.domain.ID, OperatorList{Limit: 25})
	if len(keys.Items) != 2 {
		t.Fatal("revoked rotation left key material")
	}
	// Concurrent requests with the same ID return the same durable result.
	request := auditInput()
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			if _, err := f.service.StageDKIMKey(t.Context(), f.domain.ID, request); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	keys, _ = f.store.operatorDKIMKeys(t.Context(), f.domain.ID, OperatorList{Limit: 25})
	if len(keys.Items) != 3 {
		t.Fatal("concurrent replay created multiple keys")
	}
}

func TestPGDomainLifecycleMigrationRollbackGuards(t *testing.T) {
	f := fixture(t)
	raw, _ := migrations.FS.ReadFile("20261015173147723_internal_mail_domain_lifecycle.sql")
	parts := strings.Split(string(raw), "-- +goose StatementBegin\n")
	up := strings.Split(parts[1], "-- +goose StatementEnd")[0]
	down := strings.Split(parts[2], "-- +goose StatementEnd")[0]
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), down); err != nil {
			t.Fatal("empty rollback not replay safe", err)
		}
	}
	if f.store.Ready(t.Context()) == nil {
		t.Fatal("old schema passed readiness")
	}
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), up); err != nil {
			t.Fatal("migration replay failed", err)
		}
	}
	f.service.ReverificationEnabled = func() bool { return true }
	if err := f.service.ReverifyDomains(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded authorization evidence")
	}
}

func TestPGDKIMActivationFailureAndAuditFailurePreserveSigner(t *testing.T) {
	f := domainFixture(t)
	key := verifiedKey(t, f)
	before, _ := f.store.Domain(t.Context(), f.domain.ID)
	delete(f.resolver, "_dmarc."+f.domain.Name)
	record, err := f.service.ActivateDKIMKey(t.Context(), f.domain.ID, key.ID, auditInput())
	if err != nil {
		t.Fatal(err)
	}
	var result DKIMKey
	if json.Unmarshal(record.Result, &result) != nil || result.State != "staged" || result.CheckResult != "unavailable" {
		t.Fatal("negative activation observation was not audited")
	}
	after, _ := f.store.Domain(t.Context(), f.domain.ID)
	if after.Selector != before.Selector {
		t.Fatal("failed live DNS changed active signer")
	}
	stats, err := f.store.DomainLifecycleStatistics(t.Context(), f.service.SendingIP.String())
	if err != nil || stats.FailedKeys != 1 {
		t.Fatal("failed activation missing from warning inventory", err)
	}
	f.resolver["_dmarc."+f.domain.Name] = []string{"v=DMARC1; p=none"}
	if _, err := f.service.VerifyDKIMKey(t.Context(), f.domain.ID, key.ID, auditInput()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `CREATE FUNCTION reject_domain_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit unavailable'; END $$;
CREATE TRIGGER reject_domain_audit BEFORE INSERT ON internal_mail_operations FOR EACH ROW EXECUTE FUNCTION reject_domain_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ActivateDKIMKey(t.Context(), f.domain.ID, key.ID, auditInput()); err == nil {
		t.Fatal("signer changed without audit")
	}
	after, _ = f.store.Domain(t.Context(), f.domain.ID)
	if after.Selector != before.Selector || after.DKIMKeyID != "" {
		t.Fatal("failed activation audit did not roll back signer")
	}
	keys, err := f.store.operatorDKIMKeys(t.Context(), f.domain.ID, OperatorList{Limit: 25})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys.Items {
		if k.ID == key.ID && k.State != "staged" || k.ID == f.domain.ID && k.State != "active" {
			t.Fatal("failed audit changed key history")
		}
	}
}

func TestPGDomainPeriodicChecksNeedNoCodecAndAreBounded(t *testing.T) {
	f := domainFixture(t)
	// Extra verified metadata uses the same fixture DNS proof; no real DNS or
	// private-key operations are needed to exercise the batch cursor.
	for range 12 {
		id := uuid.NewString()
		if _, err := f.pool.Exec(t.Context(), `INSERT INTO internal_mail_domains(id,namespace,name,status,selector,verification_token,public_key,sealed_key)
VALUES($1,'platform',$2,'verified',$3,'proof','public','sealed')`, id, id+".test", f.domain.Selector); err != nil {
			t.Fatal(err)
		}
	}
	f.service.Codec = nil
	candidates, err := f.store.domainCheckCandidates(t.Context(), f.service.SendingIP.String())
	if err != nil || len(candidates) != 10 {
		t.Fatal("unbounded domain pass", err)
	}
	if err := f.service.ReverifyDomains(t.Context()); err != nil {
		t.Fatal("periodic verification required private key", err)
	}
	candidates, err = f.store.domainCheckCandidates(t.Context(), f.service.SendingIP.String())
	if err != nil || len(candidates) != 2 {
		t.Fatal("domain pass repeated fresh checks or lost remaining work", len(candidates), err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := f.service.ReverifyDomains(ctx); err == nil {
		t.Fatal("cancelled verification reported success")
	}
}
