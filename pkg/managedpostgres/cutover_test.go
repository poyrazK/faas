// adr: 390 — preparation stages every binding without publishing credentials.
package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type cutoverTestSealer struct {
	calls int
	err   error
}

type cutoverCredentialSink struct {
	*bindingCredentialSink
	*cutoverTestSealer
}

func (s *cutoverTestSealer) SealCredential(_ context.Context, b Binding, m CredentialMaterial) (SealedCredential, error) {
	s.calls++
	if s.err != nil {
		return SealedCredential{}, s.err
	}
	return SealedCredential{ProviderIdentityID: m.ProviderIdentityID, Ref: "sealed-" + b.ID, Ciphertext: []byte("age-envelope"), Kid: "age-recipient", ValueHash: "hmac"}, nil
}

type failCutoverSave struct {
	CutoverStore
	fail bool
}

func (s *failCutoverSave) SaveCutoverCredential(ctx context.Context, c Cutover, m CutoverCredential, sealed SealedCredential, now time.Time) error {
	if s.fail {
		s.fail = false
		return ErrUnavailable
	}
	return s.CutoverStore.SaveCutoverCredential(ctx, c, m, sealed, now)
}
func cutoverFixture(t *testing.T) (*CutoverService, *MemoryStore, *bindingProvider, *bindingCredentialSink, *cutoverTestSealer, *time.Time, *bool, PrepareCutoverRequest) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	enabled := true
	provider := &bindingProvider{material: bindingTestMaterial()}
	provider.capabilities = testCapabilities()
	if !contains(provider.capabilities.CredentialAccess, CredentialMigration) {
		provider.capabilities.CredentialAccess = append(provider.capabilities.CredentialAccess, CredentialMigration)
	}
	registry := testRegistry(t, provider, nil)
	store := NewMemoryStore()
	source := readyHealthDatabase(t, store, registry, now)
	target := source
	target.ID = uuid.NewString()
	target.Name = "restored"
	target.ProviderResourceID = "restored-provider"
	target.RestoreSourceDatabaseID = source.ID
	target.RestoreSourceResourceID = source.ProviderResourceID
	target.RestorePointInTime = now.Add(-time.Minute)
	store.databases[target.ID] = target
	store.names[target.AccountID+"\x00"+target.Name] = target.ID
	sink := newBindingCredentialSink()
	bs, err := NewBindingService(registry, store, store, sink, BindingServiceOptions{Now: func() time.Time { return now }, ProvisioningEnabled: func() bool { return enabled }, ProviderTimeout: time.Second, LeaseDuration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	for _, access := range []CredentialAccess{CredentialReadWrite, CredentialMigration} {
		key := "DATABASE_URL"
		if access == CredentialMigration {
			key = "MIGRATION_DATABASE_URL"
		}
		if _, err := bs.Create(ctx, CreateBindingRequest{AccountID: source.AccountID, DatabaseID: source.ID, AppID: "app-a", Scope: "default", EnvironmentKey: key, Access: access}); err != nil {
			t.Fatal(err)
		}
	}
	sealer := &cutoverTestSealer{}
	service, err := NewCutoverService(bs, store, sealer)
	if err != nil {
		t.Fatal(err)
	}
	provider.issueCalls = 0
	request := PrepareCutoverRequest{ID: uuid.NewString(), AccountID: source.AccountID, AppID: "app-a", Scope: "default", SourceDatabaseID: source.ID, TargetDatabaseID: target.ID}
	return service, store, provider, sink, sealer, &now, &enabled, request
}
func TestCutoverStagesRuntimeAndMigrationTogetherAndCancelsWithGateClosed(t *testing.T) {
	ctx := context.Background()
	s, store, p, sink, _, now, enabled, r := cutoverFixture(t)
	c, err := s.Prepare(ctx, r)
	if err != nil || len(c.Credentials) != 2 || p.issueCalls != 0 {
		t.Fatalf("reserve: %+v %v", c, err)
	}
	if _, err := s.bindings.Rotate(ctx, r.AccountID, c.Credentials[0].SourceBindingID); !errors.Is(err, ErrConflict) {
		t.Fatalf("rotation bypassed cutover: %v", err)
	}
	if _, err := s.bindings.Delete(ctx, r.AccountID, c.Credentials[0].SourceBindingID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete bypassed cutover: %v", err)
	}
	if _, err := store.ClaimDelete(ctx, r.AccountID, r.TargetDatabaseID, "delete", *now, now.Add(time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("target deletion bypassed pin: %v", err)
	}
	if _, err := s.bindings.Create(ctx, CreateBindingRequest{AccountID: r.AccountID, DatabaseID: r.SourceDatabaseID, AppID: r.AppID, Scope: r.Scope, EnvironmentKey: "EXTRA_URL", Access: CredentialReadWrite}); !errors.Is(err, ErrConflict) {
		t.Fatalf("new binding changed reserved membership: %v", err)
	}
	for range 2 {
		c, err = s.Reconcile(ctx, r.AccountID, c.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if c.State != CutoverPrepared || p.issueCalls != 2 || sink.putCalls != 2 || p.lastIssueRequest.ProviderResourceID != "restored-provider" {
		t.Fatalf("preparation published or targeted source: %+v put=%d issue=%d", c, sink.putCalls, p.issueCalls)
	}
	for _, m := range c.Credentials {
		b, err := store.GetBinding(ctx, r.AccountID, m.SourceBindingID)
		if err != nil || b.DatabaseID != r.SourceDatabaseID || b.CredentialGeneration != 1 {
			t.Fatal("preparation switched source binding")
		}
	}
	payload, err := json.Marshal(c)
	if err != nil || strings.Contains(string(payload), "age-envelope") || strings.Contains(string(payload), "restored-provider") {
		t.Fatal("private stage material escaped JSON")
	}
	*enabled = false
	if _, err = s.Cancel(ctx, r.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = s.Sweep(ctx, 20); err != nil {
			t.Fatal(err)
		}
	}
	c, err = s.Get(ctx, r.AccountID, c.ID)
	if err != nil || c.State != CutoverCancelled || p.revokeCalls != 2 {
		t.Fatalf("cancel: %+v %v", c, err)
	}
	for _, m := range c.Credentials {
		if len(m.Sealed.Ciphertext) != 0 || m.State != "revoked" {
			t.Fatal("cancelled cutover retained staged secrets")
		}
	}
	if _, err = store.ClaimDelete(ctx, r.AccountID, r.TargetDatabaseID, "delete", *now, now.Add(time.Minute)); err != nil {
		t.Fatalf("cancel did not unpin target: %v", err)
	}
}
func TestCutoverCrashReplaysSameCredentialAndLateWorkerCannotPublish(t *testing.T) {
	ctx := context.Background()
	s, store, p, _, _, now, _, r := cutoverFixture(t)
	c, err := s.Prepare(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	failing := &failCutoverSave{CutoverStore: store, fail: true}
	s.store = failing
	if _, err = s.Reconcile(ctx, r.AccountID, c.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal("injected catalog outage did not fail")
	}
	first := p.lastIssueRequest
	if _, err = s.Reconcile(ctx, r.AccountID, c.ID); !errors.Is(err, ErrConflict) || p.issueCalls != 1 {
		t.Fatal("live lease allowed a second credential caller")
	}
	*now = now.Add(time.Minute + time.Second)
	c, err = s.Reconcile(ctx, r.AccountID, c.ID)
	if err != nil || p.lastIssueRequest != first || p.issueCalls != 2 {
		t.Fatalf("replay changed provider identity: %+v %v", c, err)
	}
	claim, err := store.ClaimCutover(ctx, r.AccountID, c.ID, "stale-worker", *now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Cancel(ctx, r.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	sealed := SealedCredential{ProviderIdentityID: "role", Ref: "ref", Ciphertext: []byte("age-envelope"), Kid: "age", ValueHash: "hash"}
	if err = store.SaveCutoverCredential(ctx, claim, claim.Credentials[1], sealed, *now); !errors.Is(err, ErrConflict) {
		t.Fatal("worker published after cancellation")
	}
	if _, err = s.Reconcile(ctx, r.AccountID, c.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("cancel stole an issuing worker's lease")
	}
	*now = now.Add(time.Minute + time.Second)
	for range 2 {
		_, err = s.Reconcile(ctx, r.AccountID, c.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if p.revokeCalls != 2 {
		t.Fatal("cancel omitted possibly issued, uncommitted identity")
	}
	if _, err = s.Get(ctx, "other-account", c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cutover crossed accounts")
	}
}

func TestCutoverSealingFailureStillRevokesEveryPossibleIdentity(t *testing.T) {
	ctx := context.Background()
	s, _, p, sink, sealer, now, enabled, r := cutoverFixture(t)
	c, err := s.Prepare(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	sealer.err = errors.New("seal failed with postgres://user:secret@host/db")
	if _, err = s.Reconcile(ctx, r.AccountID, c.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unsanitized stage error: %v", err)
	}
	c, err = s.Get(ctx, r.AccountID, c.ID)
	if err != nil || c.LastErrorCode != "credential_seal_failed" || p.issueCalls != 1 || sink.putCalls != 2 {
		t.Fatalf("failure: %+v %v", c, err)
	}
	*enabled = false
	if _, err = s.Cancel(ctx, r.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		c, err = s.Reconcile(ctx, r.AccountID, c.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if c.State != CutoverCancelled || p.revokeCalls != 2 {
		t.Fatal("uncommitted issuance escaped cleanup")
	}
	if _, err = s.Prepare(ctx, r); !errors.Is(err, ErrUnavailable) {
		t.Fatal("closed gate allowed credential preparation")
	}
	*enabled = true
	r.ID = uuid.NewString()
	newIntent, err := s.Prepare(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if newIntent.Credentials[0].ID == c.Credentials[0].ID {
		t.Fatal("subsequent cutover reused a revoked credential identity")
	}
	// Failed provider calls retry with stable codes and no private error text.
	p.credentialErr = errors.New("postgres://user:another-secret@host/db")
	_, err = s.Reconcile(ctx, r.AccountID, newIntent.ID)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("provider failure: %v", err)
	}
	newIntent, _ = s.Get(ctx, r.AccountID, newIntent.ID)
	if newIntent.LastErrorCode != "credential_issue_failed" || !newIntent.RetryAt.After(*now) {
		t.Fatal("provider failure did not back off")
	}
}

func TestBindingReconcilerResumesCutoverAndCancelsWithGateClosed(t *testing.T) {
	ctx := context.Background()
	s, _, provider, sink, sealer, now, enabled, request := cutoverFixture(t)
	c, err := s.Prepare(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	s.bindings.sink = &cutoverCredentialSink{bindingCredentialSink: sink, cutoverTestSealer: sealer}
	reconciler, err := NewBindingReconciler(s.bindings, BindingReconcilerOptions{
		Now: func() time.Time { return *now }, IncludeProvisioning: func() bool { return *enabled },
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = reconciler.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	c, err = s.Get(ctx, request.AccountID, c.ID)
	if err != nil || c.State != CutoverPrepared || provider.issueCalls != 2 || sink.putCalls != 2 {
		t.Fatalf("binding reconciler did not resume private staging: state=%s err=%v", c.State, err)
	}
	*enabled = false
	if _, err = s.Cancel(ctx, request.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = reconciler.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	c, err = s.Get(ctx, request.AccountID, c.ID)
	if err != nil || c.State != CutoverCancelled || provider.revokeCalls != 2 {
		t.Fatalf("binding reconciler did not finish cancellation: state=%s err=%v", c.State, err)
	}
}
