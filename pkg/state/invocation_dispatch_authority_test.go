package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
	"time"
)

type countedDispatchStore struct {
	*MemStore
	apps, authorities, owners, canaries int
	authorityErr, canaryErr             error
}

func (s *countedDispatchStore) AppByID(ctx context.Context, id string) (App, error) {
	s.apps++
	return s.MemStore.AppByID(ctx, id)
}
func (s *countedDispatchStore) invocationDispatchAuthority(ctx context.Context, id string) (invocationDispatchAuthority, error) {
	s.authorities++
	if s.authorityErr != nil {
		return invocationDispatchAuthority{}, s.authorityErr
	}
	return s.MemStore.invocationDispatchAuthority(ctx, id)
}
func (s *countedDispatchStore) InvocationEnvironmentID(ctx context.Context, id string) (string, error) {
	s.owners++
	return s.MemStore.InvocationEnvironmentID(ctx, id)
}
func (s *countedDispatchStore) GetQueueBindingCanary(ctx context.Context, account, app, id string) (api.QueueBindingCanary, error) {
	s.canaries++
	if s.canaryErr != nil {
		return api.QueueBindingCanary{}, s.canaryErr
	}
	return s.MemStore.GetQueueBindingCanary(ctx, account, app, id)
}

// ADR-570 resolves through the invocation version snapshot; count the reads
// made inside that committed view.
func (s *countedDispatchStore) WithInvocationVersionSnapshot(ctx context.Context, read func(InvocationVersionReader) error) error {
	return s.MemStore.WithInvocationVersionSnapshot(ctx, func(r InvocationVersionReader) error {
		return read(countedDispatchReader{memInvocationVersionReader: r.(memInvocationVersionReader), s: s})
	})
}

type countedDispatchReader struct {
	memInvocationVersionReader
	s *countedDispatchStore
}

func (r countedDispatchReader) AppByID(ctx context.Context, id string) (App, error) {
	r.s.apps++
	return r.memInvocationVersionReader.AppByID(ctx, id)
}
func (r countedDispatchReader) invocationDispatchAuthority(ctx context.Context, id string) (invocationDispatchAuthority, error) {
	r.s.authorities++
	if r.s.authorityErr != nil {
		return invocationDispatchAuthority{}, r.s.authorityErr
	}
	return r.memInvocationVersionReader.invocationDispatchAuthority(ctx, id)
}
func (r countedDispatchReader) InvocationEnvironmentID(ctx context.Context, id string) (string, error) {
	r.s.owners++
	return r.memInvocationVersionReader.InvocationEnvironmentID(ctx, id)
}
func (r countedDispatchReader) GetQueueBindingCanary(ctx context.Context, account, app, id string) (api.QueueBindingCanary, error) {
	r.s.canaries++
	if r.s.canaryErr != nil {
		return api.QueueBindingCanary{}, r.s.canaryErr
	}
	return r.memInvocationVersionReader.GetQueueBindingCanary(ctx, account, app, id)
}

func TestDispatchAuthorityOrdinaryReadBudget(t *testing.T) {
	for _, source := range []InvocationSource{InvocationAsyncInvoke, InvocationInboundWebhook, InvocationQueue, InvocationDelayedTask, InvocationCron, InvocationReplay} {
		t.Run(string(source), func(t *testing.T) {
			m, ctx, account, app, _ := memDeploymentFixture(t)
			inv := Invocation{ID: uuid.NewString(), AppID: app.ID, AccountID: account.ID, Source: source, State: InvocationDispatching, Payload: json.RawMessage(`{"gregale_queue_canary":"v1"}`)}
			m.invocations[inv.ID] = inv
			s := &countedDispatchStore{MemStore: m}
			if _, _, err := ResolveInvocationVersion(ctx, s, inv); err != nil {
				t.Fatal(err)
			}
			if s.apps != 1 || s.authorities != 1 || s.owners != 1 || s.canaries != 0 {
				t.Fatalf("reads: app=%d authority=%d owner=%d canary=%d", s.apps, s.authorities, s.owners, s.canaries)
			}
		})
	}
}

func dispatchCanaryFixture(t *testing.T) (*MemStore, App, Invocation) {
	t.Helper()
	ctx := context.Background()
	m := NewMemStore()
	account, err := m.CreateAccount(ctx, "dispatch@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, App{AccountID: account.ID, Slug: "dispatch", Type: AppTypeFunction, WorkloadClass: WorkloadClassHTTP, Manifest: AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	env, _ := json.Marshal(map[string]string{api.QueueCanaryCapabilityEnv: `["orders"]`})
	dep, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:dispatch", OverrideEnv: env})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.SetDeploymentRootfs(ctx, dep.ID, "/test/rootfs", "test/rootfs", 1); err != nil {
		t.Fatal(err)
	}
	if err = m.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	_, err = m.CreateQueueBindingWithConsumer(ctx, QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: WorkloadClassHTTP, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	canary, err := m.CreateQueueBindingCanary(ctx, account.ID, app.ID, api.CreateQueueBindingCanaryRequest{DeploymentID: dep.ID, Binding: "orders", TTLSeconds: 60, PublishConsent: true})
	if err != nil {
		t.Fatal(err)
	}
	inv := m.invocations[canary.ID]
	now := time.Now()
	inv.State, inv.Attempts, inv.ReceivedAt = InvocationDispatching, 1, &now
	m.invocations[inv.ID] = inv
	return m, app, inv
}

func TestDispatchAuthorityCanaryFences(t *testing.T) {
	for _, change := range []string{"valid", "esm_transport", "cancelled", "expired", "replay", "attempt", "pin", "source_spoof", "account_spoof", "app_spoof", "missing_receipt", "authority_failure", "receipt_failure", "ingress", "owner_spoof", "binding_spoof", "queue_spoof", "scope_spoof"} {
		t.Run(change, func(t *testing.T) {
			m, app, inv := dispatchCanaryFixture(t)
			s := &countedDispatchStore{MemStore: m}
			record := m.queueBindingCanaries[inv.ID]
			want := ErrConflict
			switch change {
			case "valid":
				want = nil
			case "esm_transport":
				want = nil
				inv.Source = InvocationSource("esm")
				inv.QueueName = ""
			case "cancelled":
				record.Cancelled = true
			case "expired":
				record.Report.ExpiresAt = time.Now().Add(-time.Second)
			case "replay":
				stored := m.invocations[inv.ID]
				stored.ReplayGeneration = 1
				m.invocations[inv.ID] = stored
			case "attempt":
				inv.Attempts = 2
			case "pin":
				inv.Headers = json.RawMessage(`{"X-Gregale-Revision":"` + uuid.NewString() + `"}`)
			case "source_spoof":
				record.Cancelled = true
				inv.Source = "workflow"
				inv.Payload = nil
			case "binding_spoof":
				inv.QueueBindingID = uuid.NewString()
			case "queue_spoof":
				inv.QueueName = "other"
			case "scope_spoof":
				inv.DeploymentScope = "staging"
			case "account_spoof":
				inv.AccountID = uuid.NewString()
				want = ErrNotFound
			case "app_spoof":
				other, err := m.CreateApp(context.Background(), App{AccountID: app.AccountID, Slug: "other"})
				if err != nil {
					t.Fatal(err)
				}
				inv.AppID = other.ID
			case "missing_receipt":
				s.canaryErr = ErrNotFound
				want = ErrNotFound
			case "authority_failure":
				s.authorityErr = errors.New("authority unavailable")
				want = s.authorityErr
			case "receipt_failure":
				s.canaryErr = errors.New("receipt unavailable")
				want = s.canaryErr
			case "owner_spoof":
				inv.EnvironmentID = uuid.NewString()
				want = ErrInvocationEnvironmentWorkIsolation
			}
			m.queueBindingCanaries[inv.ID] = record
			var err error
			if change == "ingress" {
				_, _, err = ResolveInvocationVersionForEnvironment(context.Background(), s, inv, "")
			} else {
				_, _, err = ResolveInvocationVersion(context.Background(), s, inv)
			}
			if !errors.Is(err, want) {
				t.Fatalf("err=%v want=%v", err, want)
			}
			// Bound queue resolution still performs its independent ownership reads.
			if (change == "valid" || change == "esm_transport") && (s.apps != 3 || s.canaries != 1 || s.authorities != 1) {
				t.Fatalf("canary reads: %+v", s)
			}
		})
	}
}

// A store without the private metadata capability must retain the conservative
// lookup, even when caller fields claim this is an unrelated source.
func TestDispatchAuthorityLegacyAdapterCannotBypassCanary(t *testing.T) {
	m, app, inv := dispatchCanaryFixture(t)
	record := m.queueBindingCanaries[inv.ID]
	record.Cancelled = true
	m.queueBindingCanaries[inv.ID] = record
	legacy := struct {
		invocationAppReader
		QueueBindingCanaryStore
	}{m, m}
	inv.Source = "cron"
	if _, _, err := ResolveInvocationVersion(context.Background(), legacy, inv); !errors.Is(err, ErrConflict) {
		t.Fatalf("legacy canary bypass: %v app=%s", err, app.ID)
	}
}
