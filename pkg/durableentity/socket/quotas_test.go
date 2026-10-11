package socket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

type quotaObject struct {
	body    []byte
	version string
}
type quotaBucket struct {
	mu        sync.Mutex
	objects   map[string]quotaObject
	revision  int
	uncertain bool
	readErr   error
}

func (b *quotaBucket) Get(_ context.Context, key string, maxBytes int64) ([]byte, string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.readErr != nil {
		return nil, "", b.readErr
	}
	v, ok := b.objects[key]
	if !ok {
		return nil, "", durableentity.ErrNotFound
	}
	if int64(len(v.body)) > maxBytes {
		return nil, "", durableentity.ErrLimit
	}
	return append([]byte(nil), v.body...), v.version, nil
}
func (b *quotaBucket) Put(_ context.Context, key string, body []byte, version string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	old, ok := b.objects[key]
	if (ok && old.version != version) || (!ok && version != "") {
		return "", durableentity.ErrConflict
	}
	b.revision++
	next := fmt.Sprint(b.revision)
	b.objects[key] = quotaObject{append([]byte(nil), body...), next}
	if b.uncertain {
		b.uncertain = false
		return "", errors.New("lost provider acknowledgement")
	}
	return next, nil
}
func quotaFixture() (*Quotas, durableentity.ID, *quotaBucket) {
	b := &quotaBucket{objects: map[string]quotaObject{}}
	q := &Quotas{Store: b, Limits: TenantLimits{Connections: 2, GrantsPerMinute: 2, InvocationsPerMinute: 2, EntityBytes: 1024, RetainedBytes: 1 << 20}}
	return q, durableentity.ID{AccountID: "account", AppID: "app", EnvironmentID: "env", TenantID: "tenant", Namespace: "rooms", Key: "document"}, b
}
func testGrant(id durableentity.ID) Grant {
	return Grant{Entity: id, IssuerID: "issuer", SessionID: uuid.NewString(), Origin: "https://example.com", Commands: []string{"open", "message"}}
}
func assertQuota(t *testing.T, err error, budget string) {
	t.Helper()
	var e *QuotaError
	if !errors.As(err, &e) || e.Budget != budget {
		t.Fatalf("want %s quota, got %v", budget, err)
	}
}
func TestTenantQuotaConnectionsAcrossNodesAndCrashExpiry(t *testing.T) {
	q, id, _ := quotaFixture()
	now := time.Now()
	q.Now = func() time.Time { return now }
	other := *q
	var wg sync.WaitGroup
	var mu sync.Mutex
	leases := []string{}
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			node := q
			if i%2 == 0 {
				node = &other
			}
			scope := id
			scope.Key = fmt.Sprint(i)
			lease, err := node.AcquireConnection(t.Context(), scope)
			if err == nil {
				mu.Lock()
				leases = append(leases, lease)
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if len(leases) != 2 {
		t.Fatalf("cross-node connections %d", len(leases))
	}
	isolated := id
	isolated.TenantID = "another-tenant"
	if _, err := other.AcquireConnection(t.Context(), isolated); err != nil {
		t.Fatal("tenant isolation", err)
	}
	if err := other.ReleaseConnection(t.Context(), id, leases[0]); err != nil {
		t.Fatal(err)
	}
	if err := q.CheckConnection(t.Context(), id, leases[0]); err == nil {
		t.Fatal("released lease admitted")
	}
	if _, err := other.AcquireConnection(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	now = now.Add(Lifetime + api.DurableEntityInvokeTimeout + WriteTimeout + IdleTimeout + time.Second)
	if err := q.CheckConnection(t.Context(), id, leases[1]); err == nil {
		t.Fatal("expired lease admitted")
	}
	if _, err := other.AcquireConnection(t.Context(), id); err != nil {
		t.Fatal("crashed capacity not reclaimed", err)
	}
}
func TestTenantQuotaGrantIssuanceRevocationAndExpiry(t *testing.T) {
	q, id, b := quotaFixture()
	now := time.Now()
	q.Now = func() time.Time { return now }
	g := &Grants{Store: b, Quotas: q}
	otherQ := *q
	other := &Grants{Store: b, Quotas: &otherQ}
	first, token, err := g.Mint(t.Context(), testGrant(id), 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.Authenticate(t.Context(), token, "https://example.com"); err != nil {
		t.Fatal("scoped credential", err)
	}
	if _, _, err = other.Mint(t.Context(), testGrant(id), 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	_, _, err = other.Mint(t.Context(), testGrant(id), time.Minute)
	assertQuota(t, err, "grant_issuance")
	if err = other.Revoke(t.Context(), first.ID, "other-account", id.AppID); !errors.Is(err, ErrGrantDenied) {
		t.Fatal("cross account revoke", err)
	}
	if err = other.Revoke(t.Context(), first.ID, id.AccountID, id.AppID); err != nil {
		t.Fatal(err)
	}
	if _, err = g.Authenticate(t.Context(), token, "https://example.com"); !errors.Is(err, ErrGrantDenied) {
		t.Fatal("cross node revocation", err)
	}
	usage, err := q.Inspect(t.Context(), id)
	if err != nil || usage.Grants != 2 || usage.GrantBytes <= 0 {
		t.Fatal(usage, err)
	}
	now = now.Add(3 * time.Minute)
	if _, _, err = other.Mint(t.Context(), testGrant(id), time.Minute); err != nil {
		t.Fatal(err)
	}
	usage, err = q.Inspect(t.Context(), id)
	if err != nil || usage.Grants != 1 || usage.GrantsIssued != 1 {
		t.Fatal("expired grant accounting", usage, err)
	}
	old := &Grants{Store: b}
	_, oldToken, err := old.Mint(t.Context(), testGrant(id), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.Authenticate(t.Context(), oldToken, "https://example.com"); !errors.Is(err, ErrGrantDenied) {
		t.Fatal("legacy credential bypassed quotas", err)
	}
}
func TestTenantQuotaInvocationRateAndRetainedEntityReservations(t *testing.T) {
	q, id, _ := quotaFixture()
	now := time.Now()
	q.Now = func() time.Time { return now }
	if err := q.AdmitInvocation(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	other := *q
	second := id
	second.Namespace = "other-namespace"
	second.Key = "other-key"
	if err := other.AdmitInvocation(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	assertQuota(t, q.AdmitInvocation(t.Context(), id), "invocations")
	now = now.Add(time.Minute)
	q.Limits.RetainedBytes = 2*1024 + 4
	other.Limits = q.Limits
	third := id
	third.Key = "third"
	assertQuota(t, other.AdmitInvocation(t.Context(), third), "retained_bytes")
	if err := q.AdmitInvocation(t.Context(), id); err != nil {
		t.Fatal("existing reservation should admit receipt replay", err)
	}
	usage, err := other.Inspect(t.Context(), id)
	if err != nil || usage.EntityReservations != 2 || usage.ReservedBytes < 2048 || usage.Invocations != 1 {
		t.Fatal(usage, err)
	}
	now = now.Add(time.Hour)
	usage, err = other.Inspect(t.Context(), id)
	if err != nil || usage.EntityReservations != 2 {
		t.Fatal("window expiry released retained state reservation", usage, err)
	}
}
func TestTenantQuotaGrantBytesShareRetainedBudget(t *testing.T) {
	q, id, b := quotaFixture()
	now := time.Now()
	q.Now = func() time.Time { return now }
	g := &Grants{Store: b, Quotas: q}
	if _, _, err := g.Mint(t.Context(), testGrant(id), time.Second); err != nil {
		t.Fatal(err)
	}
	usage, err := q.Inspect(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	q.Limits.RetainedBytes = q.Limits.EntityBytes + usage.GrantBytes - 1
	assertQuota(t, q.AdmitInvocation(t.Context(), id), "retained_bytes")
	now = now.Add(2 * time.Second)
	if err = q.AdmitInvocation(t.Context(), id); err != nil {
		t.Fatal("expired grant bytes not freed atomically", err)
	}
	usage, err = q.Inspect(t.Context(), id)
	if err != nil || usage.Grants != 0 || usage.EntityReservations != 1 {
		t.Fatal(usage, err)
	}
}
func TestTenantQuotaUncertainWritesAndCorruptionFailClosed(t *testing.T) {
	q, id, b := quotaFixture()
	q.Limits.GrantsPerMinute = 1
	b.uncertain = true
	g := &Grants{Store: b, Quotas: q}
	_, token, err := g.Mint(t.Context(), testGrant(id), time.Minute)
	if !errors.Is(err, durableentity.ErrUncertain) || token != "" {
		t.Fatal("uncertain grant acknowledged", token, err)
	}
	_, _, err = g.Mint(t.Context(), testGrant(id), time.Minute)
	assertQuota(t, err, "grant_issuance")
	usage, err := q.Inspect(t.Context(), id)
	if err != nil || usage.GrantsIssued != 1 || usage.Grants != 1 {
		t.Fatal(usage, err)
	}
	b.uncertain = true
	lease, err := q.AcquireConnection(t.Context(), id)
	if !errors.Is(err, durableentity.ErrUncertain) || lease != "" {
		t.Fatal("uncertain lease returned authority", lease, err)
	}
	// An uncertain lease is charged, but its identity is never returned.
	usage, err = q.Inspect(t.Context(), id)
	if err != nil || usage.Connections != 1 {
		t.Fatal(usage, err)
	}
	b.readErr = errors.New("provider read outage")
	if _, err = q.AcquireConnection(t.Context(), id); err == nil {
		t.Fatal("outage admitted")
	}
	b.readErr = nil
	scope, _ := scopeFor(id)
	key := "tenant-quotas/v1/" + scopeKey(scope)
	_, version, err := b.Get(t.Context(), key, maxQuotaRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Put(t.Context(), key, []byte(`{"schema":1}`), version); err != nil {
		t.Fatal(err)
	}
	if err = q.AdmitInvocation(t.Context(), id); !errors.Is(err, durableentity.ErrCorrupt) {
		t.Fatal("corrupt ledger replaced", err)
	}
}
func TestTenantQuotaWeakerNodeCannotResetPolicy(t *testing.T) {
	q, id, b := quotaFixture()
	q.Limits.GrantsPerMinute = 1
	g := &Grants{Store: b, Quotas: q}
	if _, _, err := g.Mint(t.Context(), testGrant(id), time.Minute); err != nil {
		t.Fatal(err)
	}
	weaker := *q
	weaker.Limits.GrantsPerMinute = 10
	other := &Grants{Store: b, Quotas: &weaker}
	_, _, err := other.Mint(t.Context(), testGrant(id), time.Minute)
	assertQuota(t, err, "grant_issuance")
	usage, err := weaker.Inspect(t.Context(), id)
	if err != nil || usage.Limits.GrantsPerMinute != 1 {
		t.Fatal(usage, err)
	}
	body, _ := json.Marshal(usage)
	if string(body) == "" {
		t.Fatal("missing aggregate usage")
	}
}

func TestTenantQuotaInspectDoesNotWriteOrHideExpiredStorage(t *testing.T) {
	q, id, b := quotaFixture()
	now := time.Now()
	q.Now = func() time.Time { return now }
	usage, err := q.Inspect(t.Context(), id)
	if err != nil || usage.ReservedBytes != 0 || usage.StoredGrantBytes != 0 || b.revision != 0 {
		t.Fatal("empty inspection wrote or charged usage", usage, err)
	}
	grants := &Grants{Store: b, Quotas: q}
	if _, _, err = grants.Mint(t.Context(), testGrant(id), time.Second); err != nil {
		t.Fatal(err)
	}
	revision := b.revision
	now = now.Add(2 * time.Second)
	usage, err = q.Inspect(t.Context(), id)
	if err != nil || usage.Grants != 0 || usage.GrantBytes != 0 || usage.ReservedBytes != 0 || usage.ExpiredGrants != 1 || usage.StoredGrantBytes <= 0 || b.revision != revision {
		t.Fatal("read hid physical compaction backlog or mutated authority", usage, err)
	}
}

func TestTenantQuotaLargerEntityCapCannotWeakenPersistedPolicy(t *testing.T) {
	q, id, _ := quotaFixture()
	if err := q.AdmitInvocation(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	larger := *q
	larger.Limits.EntityBytes = 2048
	other := id
	other.Key = "another-document"
	if err := larger.AdmitInvocation(t.Context(), other); !errors.Is(err, durableentity.ErrUnsupported) {
		t.Fatal("larger local execution cap bypassed persisted policy", err)
	}
	usage, err := q.Inspect(t.Context(), id)
	if err != nil || usage.EntityReservations != 1 || usage.Invocations != 1 || usage.Limits.EntityBytes != 1024 {
		t.Fatal("rejected configuration charged admission", usage, err)
	}
}
