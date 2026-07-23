package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// --- Account / Account.Active ------------------------------------------------

func TestAccountActive(t *testing.T) {
	cases := []struct {
		name   string
		status AccountStatus
		want   bool
	}{
		{"active is active", AccountActive, true},
		{"past_due is still active", AccountPastDue, true},
		{"suspended is not active", AccountSuspended, false},
		{"deleted_pending is not active", AccountDeletedPending, false},
		{"empty is not active", AccountStatus(""), false},
		{"bogus is not active", AccountStatus("zzz"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := Account{Status: tc.status}
			if got := a.Active(); got != tc.want {
				t.Errorf("Account{Status:%q}.Active() = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

// --- NewMemStore -------------------------------------------------------------

func TestNewMemStoreIsEmpty(t *testing.T) {
	m := NewMemStore()
	if m == nil {
		t.Fatal("NewMemStore returned nil")
	}
	ctx := context.Background()
	if _, err := m.AccountByEmail(ctx, "anyone@example.com"); !errors.Is(err, ErrNotFound) {
		t.Errorf("fresh store AccountByEmail: want ErrNotFound, got %v", err)
	}
	if _, err := m.AppBySlug(ctx, "anything"); !errors.Is(err, ErrNotFound) {
		t.Errorf("fresh store AppBySlug: want ErrNotFound, got %v", err)
	}
	if _, err := m.LatestDeployment(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("fresh store LatestDeployment: want ErrNotFound, got %v", err)
	}
	if _, _, err := m.GetIdempotent(ctx, "acc", "k"); !errors.Is(err, ErrNotFound) {
		t.Errorf("fresh store GetIdempotent: want ErrNotFound, got %v", err)
	}
}

// --- Accounts ----------------------------------------------------------------

func TestCreateAndLookupAccount(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	a, err := m.CreateAccount(ctx, "alice@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if a.ID == "" {
		t.Error("ID must be assigned")
	}
	if a.Email != "alice@example.com" {
		t.Errorf("Email = %q, want alice@example.com", a.Email)
	}
	if a.Plan != api.PlanHobby {
		t.Errorf("Plan = %q, want hobby", a.Plan)
	}
	if a.Status != AccountActive {
		t.Errorf("Status = %q, want active", a.Status)
	}
	if a.CreatedAt.IsZero() {
		t.Error("CreatedAt must be set")
	}

	got, err := m.AccountByEmail(ctx, "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	if got.ID != a.ID {
		t.Errorf("AccountByEmail.ID = %q, want %q", got.ID, a.ID)
	}
}

func TestCreateAccountDuplicateEmail(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	if _, err := m.CreateAccount(ctx, "dup@example.com", api.PlanFree); err != nil {
		t.Fatalf("first CreateAccount: %v", err)
	}
	_, err := m.CreateAccount(ctx, "dup@example.com", api.PlanPro)
	if err == nil {
		t.Fatal("duplicate email must error")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("exists")) {
		t.Errorf("error %q should mention 'exists'", err.Error())
	}
}

func TestAccountByKeyHash(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, err := m.CreateAccount(ctx, "kb@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	hash := []byte("0123456789abcdef")
	if _, err := m.CreateAPIKey(ctx, acc.ID, hash, "laptop"); err != nil {
		t.Fatal(err)
	}

	got, err := m.AccountByKeyHash(ctx, hash)
	if err != nil {
		t.Fatalf("AccountByKeyHash: %v", err)
	}
	if got.ID != acc.ID {
		t.Errorf("AccountByKeyHash.ID = %q, want %q", got.ID, acc.ID)
	}

	if _, err := m.AccountByKeyHash(ctx, []byte("nope")); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown hash: want ErrNotFound, got %v", err)
	}
}

// --- API keys ----------------------------------------------------------------

func TestCreateAndDeleteAPIKey(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "k@example.com", api.PlanHobby)

	hash := []byte("deadbeef")
	k, err := m.CreateAPIKey(ctx, acc.ID, hash, "ci")
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if k.ID == "" || k.AccountID != acc.ID || !bytes.Equal(k.Hash, hash) || k.Label != "ci" {
		t.Errorf("key fields wrong: %+v", k)
	}
	if k.CreatedAt.IsZero() {
		t.Error("CreatedAt must be set")
	}

	if err := m.DeleteAPIKey(ctx, acc.ID, k.ID); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}
	if _, err := m.AccountByKeyHash(ctx, hash); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete, hash lookup should be ErrNotFound, got %v", err)
	}
}

func TestCreateAPIKeyDuplicateHash(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	a1, _ := m.CreateAccount(ctx, "a1@x.com", api.PlanFree)
	a2, _ := m.CreateAccount(ctx, "a2@x.com", api.PlanFree)

	hash := []byte("samehash")
	if _, err := m.CreateAPIKey(ctx, a1.ID, hash, "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateAPIKey(ctx, a2.ID, hash, "second"); err == nil {
		t.Fatal("duplicate hash must error")
	}
}

func TestDeleteAPIKeyNotFoundAndCrossAccount(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	a1, _ := m.CreateAccount(ctx, "a1@x.com", api.PlanFree)
	a2, _ := m.CreateAccount(ctx, "a2@x.com", api.PlanFree)

	// missing key
	if err := m.DeleteAPIKey(ctx, a1.ID, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing key: want ErrNotFound, got %v", err)
	}

	// cross-account: key belongs to a1, a2 asks to delete
	k, _ := m.CreateAPIKey(ctx, a1.ID, []byte("h"), "lbl")
	if err := m.DeleteAPIKey(ctx, a2.ID, k.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-account delete: want ErrNotFound, got %v", err)
	}

	// owner can still delete after the failed cross-account attempt
	if err := m.DeleteAPIKey(ctx, a1.ID, k.ID); err != nil {
		t.Errorf("owner delete after cross-account attempt: %v", err)
	}
}

// --- Apps --------------------------------------------------------------------

func TestCreateAndLookupApp(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "appowner@x.com", api.PlanHobby)

	app, err := m.CreateApp(ctx, App{
		AccountID: acc.ID, Slug: "my-app", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
		Status: AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if app.ID == "" {
		t.Error("ID must be assigned")
	}
	if app.Status != AppActive {
		t.Errorf("Status = %q, want active", app.Status)
	}
	if app.CreatedAt.IsZero() {
		t.Error("CreatedAt must be set")
	}

	got, err := m.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if got.Slug != "my-app" {
		t.Errorf("AppByID.Slug = %q, want my-app", got.Slug)
	}

	got, err = m.AppBySlug(ctx, "my-app")
	if err != nil {
		t.Fatalf("AppBySlug: %v", err)
	}
	if got.ID != app.ID {
		t.Errorf("AppBySlug.ID = %q, want %q", got.ID, app.ID)
	}
}

func TestCreateAppDuplicateSlug(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "dup@x.com", api.PlanFree)

	if _, err := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "same"}); err != nil {
		t.Fatal(err)
	}
	_, err := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "same"})
	if err == nil {
		t.Fatal("duplicate slug must error")
	}
}

func TestCreateAppPreservesCallerIDAndCreatedAt(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "p@x.com", api.PlanFree)
	set := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	app, err := m.CreateApp(ctx, App{
		AccountID: acc.ID, Slug: "carryover",
		ID: "client-supplied-id", CreatedAt: set,
	})
	if err != nil {
		t.Fatal(err)
	}
	if app.ID != "client-supplied-id" {
		t.Errorf("ID = %q, want client-supplied-id", app.ID)
	}
	if !app.CreatedAt.Equal(set) {
		t.Errorf("CreatedAt = %v, want %v", app.CreatedAt, set)
	}
}

func TestAppBySlugAndListIgnoreDeleted(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "del@x.com", api.PlanHobby)

	a1, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "alive"})
	a2, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "doomed"})

	if err := m.DeleteApp(ctx, a2.ID); err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}

	// AppBySlug must not see deleted apps.
	if _, err := m.AppBySlug(ctx, "doomed"); !errors.Is(err, ErrNotFound) {
		t.Errorf("AppBySlug(deleted): want ErrNotFound, got %v", err)
	}

	// ListApps must not include deleted apps.
	list, err := m.ListApps(ctx, acc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != a1.ID {
		t.Errorf("ListApps = %+v, want only the alive app", list)
	}

	// Recreating the same slug after deletion must succeed (soft delete frees the slug).
	if _, err := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "doomed"}); err != nil {
		t.Errorf("re-create after delete: %v", err)
	}

	// Different account — verify isolation.
	other, _ := m.CreateAccount(ctx, "other@x.com", api.PlanFree)
	otherList, err := m.ListApps(ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(otherList) != 0 {
		t.Errorf("other account ListApps = %+v, want []", otherList)
	}
}

func TestAppByIDNotFound(t *testing.T) {
	m := NewMemStore()
	if _, err := m.AppByID(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestDeleteAppNotFound(t *testing.T) {
	m := NewMemStore()
	if err := m.DeleteApp(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

// --- Quota: CountDeployedApps ------------------------------------------------

func TestCountDeployedApps(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "q@x.com", api.PlanPro)

	// 0 deployed apps initially.
	if n, err := m.CountDeployedApps(ctx, acc.ID); err != nil || n != 0 {
		t.Fatalf("initial CountDeployedApps = (%d, %v), want (0, nil)", n, err)
	}

	a1, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "a1", Status: AppActive})
	a2, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "a2", Status: AppActive})
	a3, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "a3", Status: AppActive})

	if n, _ := m.CountDeployedApps(ctx, acc.ID); n != 3 {
		t.Errorf("three active apps: CountDeployedApps = %d, want 3", n)
	}

	// evicted_cold also occupies a slot (spec §4.2).
	a2.Status = AppEvictedCold
	m.apps[a2.ID] = a2
	if n, _ := m.CountDeployedApps(ctx, acc.ID); n != 3 {
		t.Errorf("with evicted_cold: CountDeployedApps = %d, want 3", n)
	}

	// deleted does NOT occupy a slot.
	_ = m.DeleteApp(ctx, a3.ID)
	if n, _ := m.CountDeployedApps(ctx, acc.ID); n != 2 {
		t.Errorf("after delete: CountDeployedApps = %d, want 2", n)
	}

	// Other account — isolation.
	other, _ := m.CreateAccount(ctx, "o@x.com", api.PlanFree)
	if n, _ := m.CountDeployedApps(ctx, other.ID); n != 0 {
		t.Errorf("other account: CountDeployedApps = %d, want 0", n)
	}

	_ = a1
}

// --- Deployments -------------------------------------------------------------

func TestCreateAndLatestDeployment(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "d@x.com", api.PlanHobby)
	app, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "dep-app"})

	// Unknown app must fail.
	if _, err := m.CreateDeployment(ctx, Deployment{AppID: "no-such-app"}); err == nil {
		t.Error("CreateDeployment for unknown app must error")
	}

	d1, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:1"})
	if err != nil {
		t.Fatalf("CreateDeployment d1: %v", err)
	}
	if d1.ID == "" || d1.CreatedAt.IsZero() {
		t.Errorf("d1 fields not initialized: %+v", d1)
	}
	// Force a later CreatedAt on d2 so Latest is unambiguous.
	d2, err := m.CreateDeployment(ctx, Deployment{
		AppID: app.ID, ImageDigest: "sha256:2",
		CreatedAt: time.Now().Add(time.Second),
	})
	if err != nil {
		t.Fatalf("CreateDeployment d2: %v", err)
	}

	latest, err := m.LatestDeployment(ctx, app.ID)
	if err != nil {
		t.Fatalf("LatestDeployment: %v", err)
	}
	if latest.ID != d2.ID {
		t.Errorf("LatestDeployment.ID = %q, want %q", latest.ID, d2.ID)
	}
}

func TestCreateDeploymentPreservesCallerFields(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "p2@x.com", api.PlanFree)
	app, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "preserve"})
	set := time.Date(2025, 6, 7, 8, 9, 10, 0, time.UTC)

	d, err := m.CreateDeployment(ctx, Deployment{
		AppID: app.ID, ImageDigest: "sha256:x",
		ID: "client-id", CreatedAt: set,
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != "client-id" {
		t.Errorf("ID = %q, want client-id", d.ID)
	}
	if !d.CreatedAt.Equal(set) {
		t.Errorf("CreatedAt = %v, want %v", d.CreatedAt, set)
	}
}

func TestLatestDeploymentNotFound(t *testing.T) {
	m := NewMemStore()
	if _, err := m.LatestDeployment(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

// --- Idempotency -------------------------------------------------------------

func TestIdempotencyPutGetRoundTrip(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	body := []byte(`{"ok":true}`)

	if err := m.PutIdempotent(ctx, "acc", "k", 201, body); err != nil {
		t.Fatalf("PutIdempotent: %v", err)
	}
	status, got, err := m.GetIdempotent(ctx, "acc", "k")
	if err != nil {
		t.Fatalf("GetIdempotent: %v", err)
	}
	if status != 201 {
		t.Errorf("status = %d, want 201", status)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("body = %q, want %q", got, body)
	}
}

func TestIdempotencyGetMisses(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	if _, _, err := m.GetIdempotent(ctx, "acc", "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing key: want ErrNotFound, got %v", err)
	}

	// Different account, same key — must be isolated.
	if err := m.PutIdempotent(ctx, "acc1", "k", 200, []byte("a")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.GetIdempotent(ctx, "acc2", "k"); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-account: want ErrNotFound, got %v", err)
	}
}

func TestIdempotencyPutDefensivelyCopiesBody(t *testing.T) {
	// Spec invariant: PutIdempotent must not alias the caller's slice.
	m := NewMemStore()
	ctx := context.Background()
	body := []byte("original")
	if err := m.PutIdempotent(ctx, "acc", "k", 200, body); err != nil {
		t.Fatal(err)
	}
	body[0] = 'X' // mutate after Put
	_, got, err := m.GetIdempotent(ctx, "acc", "k")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Errorf("stored body = %q, want %q (PutIdempotent must defensively copy)", got, "original")
	}
}

func TestIdempotencyOverwrite(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	if err := m.PutIdempotent(ctx, "acc", "k", 200, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := m.PutIdempotent(ctx, "acc", "k", 500, []byte("second")); err != nil {
		t.Fatal(err)
	}
	status, body, err := m.GetIdempotent(ctx, "acc", "k")
	if err != nil {
		t.Fatal(err)
	}
	if status != 500 || string(body) != "second" {
		t.Errorf("overwrite: got (%d, %q), want (500, %q)", status, body, "second")
	}
}

// TestDeploymentLogsAppendAndPage is the M7.5 slice 5 contract:
// every insert returns a monotonic seq, ListDeploymentLogs returns
// the rows DESC by seq, paging by `seq < before` works, and hasMore
// is true iff an older row sits behind the page.
func TestDeploymentLogsAppendAndPage(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	for i := 0; i < 200; i++ {
		seq, err := m.AppendDeploymentLog(ctx, "dep-1", "stdout", lineN(i))
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		if seq != int64(i+1) {
			t.Errorf("append %d seq = %d, want %d", i, seq, i+1)
		}
	}

	// First page: newest first.
	page, hasMore, err := m.ListDeploymentLogs(ctx, "dep-1", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 50 {
		t.Fatalf("first page size = %d, want 50", len(page))
	}
	if !hasMore {
		t.Errorf("first page hasMore = false, want true (200 rows > 50)")
	}
	if page[0].Seq != 200 || page[49].Seq != 151 {
		t.Errorf("page seq range = [%d, %d], want [200, 151]", page[0].Seq, page[49].Seq)
	}

	// Page 2: before the first row's seq boundary.
	page2, hasMore2, err := m.ListDeploymentLogs(ctx, "dep-1", page[49].Seq, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 50 || page2[0].Seq != 150 || page2[49].Seq != 101 {
		t.Errorf("page2 seq range = [%d, %d], want [150, 101]", page2[0].Seq, page2[49].Seq)
	}
	if !hasMore2 {
		t.Errorf("page2 hasMore = false, want true")
	}

	// Last page: rows 100..51 returned, hasMore=true (rows 50..1 remain).
	page3, hasMore3, err := m.ListDeploymentLogs(ctx, "dep-1", page2[49].Seq, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page3) != 50 || !hasMore3 {
		t.Errorf("page3 len=%d hasMore=%v, want 50/true", len(page3), hasMore3)
	}
	// Past the second-to-last page: rows 50..1.
	page4, hasMore4, err := m.ListDeploymentLogs(ctx, "dep-1", page3[49].Seq, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page4) != 50 || hasMore4 {
		t.Errorf("page4 len=%d hasMore=%v, want 50/false (no rows behind seq=1)", len(page4), hasMore4)
	}
	// Past the oldest row: empty.
	page5, hasMore5, err := m.ListDeploymentLogs(ctx, "dep-1", 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page5) != 0 || hasMore5 {
		t.Errorf("page5 len=%d hasMore=%v, want 0/false", len(page5), hasMore5)
	}
}

// TestDeploymentLogsUnknownDeployment covers the empty-row path —
// the SSE handler always opens with a page, even when nothing has
// been logged yet.
func TestDeploymentLogsUnknownDeployment(t *testing.T) {
	m := NewMemStore()
	page, hasMore, err := m.ListDeploymentLogs(context.Background(), "missing", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 0 || hasMore {
		t.Errorf("unknown dep page = (%d, hasMore=%v), want (0, false)", len(page), hasMore)
	}
}

// TestDeploymentLogsLimitClamp asserts the safe-by-default guard
// against caller-supplied limit values (CodeQL go/allocation-size).
// A hostile caller that forgets to clamp `limit` must not be able
// to trigger an oversized slice allocation.
func TestDeploymentLogsLimitClamp(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	dep := "dep-clamp"
	for i := 0; i < MaxDeploymentLogPage*2; i++ {
		if _, err := m.AppendDeploymentLog(ctx, dep, "stdout", lineN(i)); err != nil {
			t.Fatal(err)
		}
	}
	// Caller requests 1_000_000 rows → must clamp to MaxDeploymentLogPage.
	page, hasMore, err := m.ListDeploymentLogs(ctx, dep, 0, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != MaxDeploymentLogPage {
		t.Errorf("clamped page len = %d, want %d", len(page), MaxDeploymentLogPage)
	}
	if !hasMore {
		t.Errorf("hasMore = false; expected true (rows remain past the clamp)")
	}
}

func lineN(i int) string {
	return "line" + itoaSmall(i)
}

func itoaSmall(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [4]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}

// TestMemStore_GitHubBinding_RoundTrip exercises the binding
// persistence + reverse-lookup added for review finding #1+#2
// closure (migration 00007). Asserts:
//
//   - RecordGitHubBinding persists across GetGitHubBindingForApp
//   - InstallationIDForRepo (the new reverse lookup) returns the
//     right id for a bound repo
//   - ErrNotFound for an unbound repo (this is the §11 fail-closed
//     path: checks.go must NOT fall back to install_id=1 when no
//     app is bound)
func TestMemStore_GitHubBinding_RoundTrip(t *testing.T) {
	store := NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "alice@example.com", "free")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(context.Background(), App{AccountID: acct.ID, Slug: "api"})
	if err != nil {
		t.Fatal(err)
	}

	if err := store.RecordGitHubBinding(context.Background(), app.ID, 4242, "octo/api", "main"); err != nil {
		t.Fatalf("RecordGitHubBinding: %v", err)
	}

	b, err := store.GitHubBindingForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("GitHubBindingForApp: %v", err)
	}
	if b.InstallID != 4242 {
		t.Errorf("InstallID = %d, want 4242", b.InstallID)
	}
	if b.RepoFullName != "octo/api" {
		t.Errorf("RepoFullName = %q, want octo/api", b.RepoFullName)
	}

	id, err := store.InstallationIDForRepo(context.Background(), "octo/api")
	if err != nil {
		t.Fatalf("InstallationIDForRepo: %v", err)
	}
	if id != 4242 {
		t.Errorf("install id for octo/api = %d, want 4242", id)
	}

	// Unbound repo → ErrNotFound (NOT a hardcoded id=1).
	_, err = store.InstallationIDForRepo(context.Background(), "octo/unbound")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound for unbound repo", err)
	}

	// Empty repo → ErrNotFound (defensive).
	_, err = store.InstallationIDForRepo(context.Background(), "")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound for empty repo", err)
	}
}

// TestMemStore_GitHubBinding_RejectsConflict mirrors the migration's
// apps_github_install_repo_uniq partial index: two apps cannot claim
// the same (install_id, repo) pair. The §11 least-privilege audit
// invariant lives on this constraint.
func TestMemStore_GitHubBinding_RejectsConflict(t *testing.T) {
	store := NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "alice@example.com", "free")
	if err != nil {
		t.Fatal(err)
	}
	app1, err := store.CreateApp(context.Background(), App{AccountID: acct.ID, Slug: "api1"})
	if err != nil {
		t.Fatal(err)
	}
	app2, err := store.CreateApp(context.Background(), App{AccountID: acct.ID, Slug: "api2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordGitHubBinding(context.Background(), app1.ID, 1, "octo/api", "main"); err != nil {
		t.Fatalf("first binding: %v", err)
	}
	err = store.RecordGitHubBinding(context.Background(), app2.ID, 1, "octo/api", "main")
	if err == nil {
		t.Fatal("expected conflict error when second app tries to bind same (install_id, repo)")
	}
}

// --- Customer secrets (spec §11/G2) ----------------------------------------

// TestAppSecretUpsertListDelete exercises the four-method CRUD through
// MemStore. Ciphertext is opaque bytes here — the MemStore's job is to
// model the (account_id, app_id, key) row shape; pgstore mirrors the same
// SQL semantics. Encryption is pkg/secretbox's responsibility.
func TestAppSecretUpsertListDelete(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	const acctA, acctB = "acct-A", "acct-B"
	const appA, appB = "app-A", "app-B"

	// Initial state: nothing.
	got, err := m.ListAppSecrets(ctx, acctA, appA)
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty list: got %d, want 0", len(got))
	}

	// Upsert two keys under (acctA, appA).
	if err := m.UpsertAppSecret(ctx, acctA, appA, "STRIPE_KEY", []byte("cipher-1")); err != nil {
		t.Fatalf("upsert STRIPE_KEY: %v", err)
	}
	if err := m.UpsertAppSecret(ctx, acctA, appA, "API_TOKEN", []byte("cipher-2")); err != nil {
		t.Fatalf("upsert API_TOKEN: %v", err)
	}

	// Count is 2.
	n, err := m.CountAppSecrets(ctx, acctA, appA)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("count: got %d, want 2", n)
	}

	// List returns both, sorted by key (API_TOKEN before STRIPE_KEY).
	got, err = m.ListAppSecrets(ctx, acctA, appA)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("list: got %d, want 2", len(got))
	}
	if got[0].Key != "API_TOKEN" || got[1].Key != "STRIPE_KEY" {
		t.Errorf("order: got %q/%q, want API_TOKEN/STRIPE_KEY", got[0].Key, got[1].Key)
	}

	// Upsert replaces ciphertext on conflict (same key).
	if err := m.UpsertAppSecret(ctx, acctA, appA, "API_TOKEN", []byte("cipher-2-rotated")); err != nil {
		t.Fatalf("upsert rotate: %v", err)
	}
	got, _ = m.ListAppSecrets(ctx, acctA, appA)
	if string(got[0].Ciphertext) != "cipher-2-rotated" {
		t.Errorf("rotate: got %q, want cipher-2-rotated", string(got[0].Ciphertext))
	}

	// Cross-account isolation: acctB sees nothing on appA.
	if n, _ := m.CountAppSecrets(ctx, acctB, appA); n != 0 {
		t.Errorf("cross-acct count: got %d, want 0", n)
	}
	if got, _ := m.ListAppSecrets(ctx, acctB, appA); len(got) != 0 {
		t.Errorf("cross-acct list: got %d, want 0", len(got))
	}

	// Cross-app isolation: same account, different app.
	if err := m.UpsertAppSecret(ctx, acctA, appB, "DB_URL", []byte("cipher-3")); err != nil {
		t.Fatalf("upsert appB: %v", err)
	}
	if n, _ := m.CountAppSecrets(ctx, acctA, appB); n != 1 {
		t.Errorf("appB count: got %d, want 1", n)
	}
	if n, _ := m.CountAppSecrets(ctx, acctA, appA); n != 2 {
		t.Errorf("appA count after appB upsert: got %d, want 2", n)
	}

	// Delete scoped to (acctA, appA, STRIPE_KEY).
	if err := m.DeleteAppSecret(ctx, acctA, appA, "STRIPE_KEY"); err != nil {
		t.Fatalf("delete STRIPE_KEY: %v", err)
	}
	if n, _ := m.CountAppSecrets(ctx, acctA, appA); n != 1 {
		t.Errorf("post-delete count: got %d, want 1", n)
	}

	// Delete on cross-account returns ErrNotFound (renders 400 CodeSecretNotFound).
	if err := m.DeleteAppSecret(ctx, acctB, appA, "API_TOKEN"); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-acct delete: got %v, want ErrNotFound", err)
	}

	// Delete on unknown key returns ErrNotFound.
	if err := m.DeleteAppSecret(ctx, acctA, appA, "MISSING"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing delete: got %v, want ErrNotFound", err)
	}
}

// TestAppSecretOwnershipOnUpsert asserts the (account_id, app_id, key) is
// the unique row identifier: a different account's upsert against the same
// (app_id, key) returns ErrNotFound (treated as "row not yours" by the
// handler). This matches the SQL semantics where the PRIMARY KEY is on
// (app_id, key) but the ownership check happens via account_id at the
// query layer.
func TestAppSecretOwnershipOnUpsert(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	// acctA owns the row.
	if err := m.UpsertAppSecret(ctx, "acct-A", "app-1", "K", []byte("c1")); err != nil {
		t.Fatalf("upsert A: %v", err)
	}
	// acctB tries to overwrite — gets ErrNotFound (handler renders 400).
	if err := m.UpsertAppSecret(ctx, "acct-B", "app-1", "K", []byte("c2")); !errors.Is(err, ErrNotFound) {
		t.Errorf("acctB upsert: got %v, want ErrNotFound", err)
	}
	// Original row untouched.
	got, _ := m.ListAppSecrets(ctx, "acct-A", "app-1")
	if len(got) != 1 || string(got[0].Ciphertext) != "c1" {
		t.Errorf("row integrity: got %+v, want c1", got)
	}
}

// --- G6 GDPR self-service regressions ----------------------------------------

// TestMem_DeleteAccount_CascadesEvents is the MemStore half of the G6
// right-to-erasure regression (spec §17 G6, ADR-021). Audit events
// whose subject is the account id, or whose payload account_id
// matches, must not survive DeleteAccount.
func TestMem_DeleteAccount_CascadesEvents(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "events@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	// DeleteAccount is conditional on status='deleted_pending'; mark
	// pending first so the cascade actually runs (mirrors the grace
	// timer's pre-condition).
	if err := m.MarkAccountDeletionPending(ctx, acct.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	// Subject-keyed event: subject == acct.ID.
	subject := acct.ID
	if err := m.AppendEvent(ctx, "test", "export", &subject, []byte(`{}`)); err != nil {
		t.Fatalf("AppendEvent subject: %v", err)
	}
	// Data-keyed event: data.account_id == acct.ID.
	payload := []byte(`{"account_id":"` + acct.ID + `"}`)
	if err := m.AppendEvent(ctx, "test", "export", nil, payload); err != nil {
		t.Fatalf("AppendEvent data: %v", err)
	}
	// Surviving event (different account) — must NOT be touched.
	other := "00000000-0000-0000-0000-000000000099"
	if err := m.AppendEvent(ctx, "test", "export", nil,
		[]byte(`{"account_id":"`+other+`"}`)); err != nil {
		t.Fatalf("AppendEvent other: %v", err)
	}

	if err := m.DeleteAccount(ctx, acct.ID); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	// ListEvents returns m.events; nothing here filters by subject so
	// we walk the slice directly to assert both erasure predicates ran.
	idUUID := uuid.MustParse(acct.ID)
	for _, e := range m.events {
		if e.Subject != nil && *e.Subject == idUUID {
			t.Errorf("subject-keyed event survived DeleteAccount: %+v", e)
		}
		if len(e.Data) > 0 {
			var got map[string]string
			if jerr := json.Unmarshal(e.Data, &got); jerr == nil &&
				got["account_id"] == acct.ID {
				t.Errorf("data-keyed event survived DeleteAccount: %+v", e)
			}
		}
	}
	// Surviving-event sanity: the other account's audit row is still
	// in the slice.
	var sawOther bool
	for _, e := range m.events {
		if len(e.Data) == 0 {
			continue
		}
		var got map[string]string
		if json.Unmarshal(e.Data, &got) == nil && got["account_id"] == other {
			sawOther = true
		}
	}
	if !sawOther {
		t.Errorf("unrelated event was collateral damage")
	}
}

// TestMem_DeleteAccount_OnActiveRowReturnsErrNotFound is the
// MemStore half of the conditional-DELETE sentinel regression (review
// of #46). Before the patch, DeleteAccount ran an unconditional
// `delete from accounts` and then a probe — so a redelivered tick on
// an already-restored row reported success and the grace timer's
// `errors.Is(err, ErrNotFound)` branch was dead code. The new
// conditional matches the PG SQL and returns ErrNotFound.
func TestMem_DeleteAccount_OnActiveRowReturnsErrNotFound(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "active@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	err = m.DeleteAccount(ctx, acct.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteAccount on active row = %v, want ErrNotFound", err)
	}
	if _, err := m.AccountByID(ctx, acct.ID); err != nil {
		t.Errorf("AccountByID after no-op delete = %v, want nil", err)
	}
}

// TestMem_DeleteAccount_TwiceIsErrNotFound covers the idempotent
// retry path: the second call must report ErrNotFound, not silently
// succeed.
func TestMem_DeleteAccount_TwiceIsErrNotFound(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "twice@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := m.MarkAccountDeletionPending(ctx, acct.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	if err := m.DeleteAccount(ctx, acct.ID); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	err = m.DeleteAccount(ctx, acct.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v, want ErrNotFound", err)
	}
}

// --- RenameApp (issue #63) --------------------------------------------------
//
// These tests lock down the MemStore contract that the apid handler relies
// on (handlers_ext.go:247 errors.Is(err, state.ErrConflict)) and that the
// PgStore must mirror via mapErr → unique-violation SQLSTATE. They run as
// pure in-memory tests, so they're part of `make test` (no KVM, no real
// Postgres). The PgStore equivalents live in pgstore_test.go behind
// pgtest.Open — the two together pin the error contract.

func TestMem_RenameApp_HappyPath(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "rename@x.com", api.PlanHobby)
	app, err := m.CreateApp(ctx, App{
		AccountID: acc.ID, Slug: "old-name", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60, Status: AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	got, err := m.RenameApp(ctx, acc.ID, "old-name", "new-name")
	if err != nil {
		t.Fatalf("RenameApp: %v", err)
	}
	if got.Slug != "new-name" {
		t.Errorf("Slug = %q, want new-name", got.Slug)
	}
	if got.ID != app.ID {
		t.Errorf("ID = %q, want %q (same row, mutated in place)", got.ID, app.ID)
	}

	// Old slug must be gone from the lookup table.
	if _, err := m.AppBySlug(ctx, "old-name"); !errors.Is(err, ErrNotFound) {
		t.Errorf("AppBySlug(old-name) = %v, want ErrNotFound", err)
	}
	// New slug must resolve.
	if back, err := m.AppBySlug(ctx, "new-name"); err != nil {
		t.Errorf("AppBySlug(new-name): %v", err)
	} else if back.ID != app.ID {
		t.Errorf("AppBySlug(new-name).ID = %q, want %q", back.ID, app.ID)
	}
}

func TestMem_RenameApp_SlugTakenReturnsErrConflict(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "take@x.com", api.PlanHobby)
	if _, err := m.CreateApp(ctx, App{
		AccountID: acc.ID, Slug: "victim", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, Status: AppActive,
	}); err != nil {
		t.Fatalf("CreateApp victim: %v", err)
	}
	if _, err := m.CreateApp(ctx, App{
		AccountID: acc.ID, Slug: "blocker", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, Status: AppActive,
	}); err != nil {
		t.Fatalf("CreateApp blocker: %v", err)
	}

	_, err := m.RenameApp(ctx, acc.ID, "victim", "blocker")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("RenameApp onto existing slug = %v, want ErrConflict", err)
	}

	// The losing rename must not have moved the victim.
	if _, err := m.AppBySlug(ctx, "victim"); err != nil {
		t.Errorf("victim disappeared after failed rename: %v", err)
	}
	if _, err := m.AppBySlug(ctx, "blocker"); err != nil {
		t.Errorf("blocker disappeared after failed rename: %v", err)
	}
}

func TestMem_RenameApp_UnknownSlugReturnsErrNotFound(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "ghost@x.com", api.PlanHobby)
	if _, err := m.CreateApp(ctx, App{
		AccountID: acc.ID, Slug: "real", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, Status: AppActive,
	}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	_, err := m.RenameApp(ctx, acc.ID, "ghost", "anything")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("RenameApp on missing slug = %v, want ErrNotFound", err)
	}
}

// TestMem_RenameApp_CrossAccountIsolation pins the (account_id, slug)
// pair in the source lookup: account A must not be able to mutate an
// app that belongs to account B. Attempting to rename B's slug from
// A's context looks the same as "no such slug in this account" →
// ErrNotFound. Without the accountID scope in the WHERE clause, this
// would be a horizontal-priv-esc.
//
// The collision direction (A trying to rename alpha → beta where B owns
// beta) is a SEPARATE concern — slug namespacing is global by design
// (apps.slug is a unique constraint, same as CreateApp). Probing for
// foreign slugs via rename collisions is a known enumeration surface
// that mirrors CreateApp's; not in scope for this test.
func TestMem_RenameApp_CrossAccountIsolation(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	accA, _ := m.CreateAccount(ctx, "a@x.com", api.PlanHobby)
	accB, _ := m.CreateAccount(ctx, "b@x.com", api.PlanHobby)

	if _, err := m.CreateApp(ctx, App{
		AccountID: accA.ID, Slug: "alpha", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, Status: AppActive,
	}); err != nil {
		t.Fatalf("CreateApp A: %v", err)
	}
	if _, err := m.CreateApp(ctx, App{
		AccountID: accB.ID, Slug: "beta", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, Status: AppActive,
	}); err != nil {
		t.Fatalf("CreateApp B: %v", err)
	}

	// A cannot rename B's slug — must look like ErrNotFound, not
	// ErrConflict (which would leak existence info about B's app).
	if _, err := m.RenameApp(ctx, accA.ID, "beta", "stolen"); !errors.Is(err, ErrNotFound) {
		t.Errorf("A renaming B's slug = %v, want ErrNotFound (account scope on source lookup)", err)
	}
	// Symmetric: A also cannot touch B's slug when asking for an
	// unrelated rename target.
	if _, err := m.RenameApp(ctx, accA.ID, "beta", "renamed-beta"); !errors.Is(err, ErrNotFound) {
		t.Errorf("A renaming B's slug (any target) = %v, want ErrNotFound", err)
	}

	// B's app must still exist under the original slug.
	if got, err := m.AppBySlug(ctx, "beta"); err != nil {
		t.Errorf("B's beta vanished after failed cross-account rename: %v", err)
	} else if got.AccountID != accB.ID {
		t.Errorf("B's beta reassigned to %q after cross-account rename attempt", got.AccountID)
	}
	// A's app must not have been touched either.
	if got, err := m.AppBySlug(ctx, "alpha"); err != nil {
		t.Errorf("A's alpha vanished after cross-account attempt: %v", err)
	} else if got.AccountID != accA.ID {
		t.Errorf("A's alpha reassigned: %v", err)
	}
}

// --- snapshot GC tests -----------------------------------------------------

func TestMemStore_ListSnapshotsForGC(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, _ := m.CreateAccount(ctx, "u@example.com", "pro")
	app, _ := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "snap", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	depA, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:a"})
	depB, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:b"})
	if _, err := m.CreateSnapshot(ctx, Snapshot{
		DeploymentID: depA.ID, MemBytes: 100, DiskBytes: 100,
		FCVersion:  "1.8.0",
		StorageKey: SnapMemKey(depA.ID),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateSnapshot(ctx, Snapshot{
		DeploymentID: depB.ID, MemBytes: 200, DiskBytes: 200,
		FCVersion:  "1.8.0",
		StorageKey: SnapMemKey(depB.ID),
	}); err != nil {
		t.Fatal(err)
	}

	rows, err := m.ListSnapshotsForGC(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Errorf("ListSnapshotsForGC returned %d, want 2", len(rows))
	}
	for _, r := range rows {
		if r.AppID != app.ID {
			t.Errorf("row AppID = %q, want %q", r.AppID, app.ID)
		}
		if r.AccountID != acct.ID {
			t.Errorf("row AccountID = %q, want %q", r.AccountID, acct.ID)
		}
	}
}

func TestMemStore_ListSnapshotsForGC_ExcludesDeletedApp(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, _ := m.CreateAccount(ctx, "u@example.com", "pro")
	app, _ := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "del-app", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	dep, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:a"})
	if _, err := m.CreateSnapshot(ctx, Snapshot{
		DeploymentID: dep.ID, MemBytes: 100, DiskBytes: 100,
		FCVersion:  "1.8.0",
		StorageKey: SnapMemKey(dep.ID),
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteApp(ctx, app.ID); err != nil {
		t.Fatal(err)
	}

	rows, _ := m.ListSnapshotsForGC(ctx)
	if len(rows) != 0 {
		t.Errorf("deleted app's snapshot leaked into GC: %d rows", len(rows))
	}
}

func TestMemStore_DeleteSnapshotsByID_BulkAndIdempotent(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, _ := m.CreateAccount(ctx, "u@example.com", "pro")
	app, _ := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "del-snap", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	depA, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:a"})
	depB, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:b"})
	snapA, _ := m.CreateSnapshot(ctx, Snapshot{
		DeploymentID: depA.ID, MemBytes: 100, DiskBytes: 100, FCVersion: "1.8.0",
		StorageKey: SnapMemKey(depA.ID),
	})
	snapB, _ := m.CreateSnapshot(ctx, Snapshot{
		DeploymentID: depB.ID, MemBytes: 100, DiskBytes: 100, FCVersion: "1.8.0",
		StorageKey: SnapMemKey(depB.ID),
	})

	n, err := m.DeleteSnapshotsByID(ctx, []string{snapA.ID, snapB.ID})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("first delete = %d, want 2", n)
	}
	n2, err := m.DeleteSnapshotsByID(ctx, []string{snapA.ID, snapB.ID})
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Errorf("second delete = %d, want 0 (idempotent)", n2)
	}
}

func TestMemStore_MarkAllSnapshotsStaleByFCVersion(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, _ := m.CreateAccount(ctx, "u@example.com", "pro")
	app, _ := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "fc-sweep", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	insert := func(v string) string {
		dep, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:" + v})
		snap, _ := m.CreateSnapshot(ctx, Snapshot{
			DeploymentID: dep.ID, MemBytes: 100, DiskBytes: 100,
			FCVersion:  v,
			StorageKey: SnapMemKey(dep.ID),
		})
		return snap.ID
	}
	insert("1.7.0")
	insert("1.8.0")
	insert("1.9.0")

	n, err := m.MarkAllSnapshotsStaleByFCVersion(ctx, "1.8.0")
	if err != nil {
		t.Fatal(err)
	}
	// Both 1.7 and 1.9 are NOT 1.8 → marked stale. Only the current
	// FC version's snapshots stay live.
	if n != 2 {
		t.Errorf("marked %d stale, want 2", n)
	}
	// Idempotent: a second call finds no non-stale rows to flip.
	n2, _ := m.MarkAllSnapshotsStaleByFCVersion(ctx, "1.8.0")
	if n2 != 0 {
		t.Errorf("second sweep marked %d, want 0", n2)
	}
}

func TestMemStore_MarkOldSnapshotsStale(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, _ := m.CreateAccount(ctx, "u@example.com", "pro")
	app, _ := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "mark-old", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	depA, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:a"})
	depB, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:b"})
	snapA, _ := m.CreateSnapshot(ctx, Snapshot{
		DeploymentID: depA.ID, MemBytes: 100, DiskBytes: 100, FCVersion: "1.8.0",
		StorageKey: SnapMemKey(depA.ID),
	})
	_, _ = m.CreateSnapshot(ctx, Snapshot{
		DeploymentID: depB.ID, MemBytes: 100, DiskBytes: 100, FCVersion: "1.8.0",
		StorageKey: SnapMemKey(depB.ID),
	})

	n, err := m.MarkOldSnapshotsStale(ctx, []string{snapA.ID})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("marked %d, want 1", n)
	}
	// LatestSnapshot filters stale out; inspect the row directly.
	foundA, foundB := false, false
	for _, s := range m.snapshots {
		if s.ID == snapA.ID {
			foundA = true
			if !s.Stale {
				t.Errorf("snapA not marked stale")
			}
		}
		if s.ID != snapA.ID && s.DeploymentID == depB.ID {
			foundB = true
			if s.Stale {
				t.Errorf("snapB marked stale by an unrelated call")
			}
		}
	}
	if !foundA || !foundB {
		t.Errorf("seed snapshots missing from store: A=%v B=%v", foundA, foundB)
	}
}

// TestMemStore_SnapshotStorageKey_RoundTrip pins the #96 / ADR-025
// axis 2 storage_key field: CreateSnapshot stores the value the
// caller passes, LatestSnapshot reads it back unchanged, and
// ListSnapshotsForGC exposes it on SnapshotForGC so the imaged GC
// can Storage.Delete under the canonical key.
// --- Compute nodes (issue #97 / ADR-025 axis 3) -----------------------------
//
// The MemStore seeds a synthetic 'default-local' node on NewMemStore()
// (memstore.go:seedDefaultLocalNodeLocked) so any caller that needs the
// canonical default-local id can fetch it via ComputeNodeByName without
// seeding first. Tests below rely on that — they do NOT call seedDefault.

func TestMem_ComputeNodes_DefaultLocalSeededOnNewStore(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	// The seeded node is the synthetic default-local: active=true,
	// target URL the legacy unix socket, admission ceiling the legacy
	// 47,600 MB. The id is non-empty and the name matches
	// DefaultLocalNodeName (the canonical name callers resolve against).
	got, err := m.ComputeNodeByName(ctx, DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("ComputeNodeByName(default-local): %v", err)
	}
	if got.Name != DefaultLocalNodeName {
		t.Errorf("Name=%q, want %q", got.Name, DefaultLocalNodeName)
	}
	if !got.Active {
		t.Errorf("seeded default-local should be active, got %v", got.Active)
	}
	if got.AdmissionCeilingMB != 47600 {
		t.Errorf("AdmissionCeilingMB=%d, want 47600", got.AdmissionCeilingMB)
	}
	if got.TargetURL != "unix:///run/faas/vmmd.sock" {
		t.Errorf("TargetURL=%q, want %q", got.TargetURL, "unix:///run/faas/vmmd.sock")
	}
	if got.LastHeartbeatAt.IsZero() {
		t.Errorf("seeded LastHeartbeatAt should be stamped at creation")
	}
}

func TestMem_ComputeNodes_NewMemStoreSeedsDefaultLocal(t *testing.T) {
	// Pin the seeding invariant: NewMemStore() must place the synthetic
	// default-local row in ActiveComputeNodes immediately, NOT on first
	// read. schedd's startup path depends on this — cmd/schedd/main.go's
	// runHeartbeat calls ComputeNodeByName once at boot and treats a
	// non-row result as a loud failure. A future refactor that lazily
	// seeds on first read would silently break that contract; this test
	// surfaces the regression before it lands.
	m := NewMemStore()
	nodes, err := m.ActiveComputeNodes(context.Background())
	if err != nil {
		t.Fatalf("ActiveComputeNodes: %v", err)
	}
	found := false
	for _, n := range nodes {
		if n.Name == DefaultLocalNodeName {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, 0, len(nodes))
		for _, n := range nodes {
			names = append(names, n.Name)
		}
		t.Errorf("default-local missing from ActiveComputeNodes after NewMemStore (got %v)", names)
	}
}

func TestMem_ComputeNodes_ActiveComputeNodes_OnlyReturnsActive(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	// Create one active and one drained node.
	active := state_computeNodeFixture("active-node", true)
	drained := state_computeNodeFixture("drained-node", false)
	if _, err := m.CreateComputeNode(ctx, active); err != nil {
		t.Fatalf("CreateComputeNode(active): %v", err)
	}
	if _, err := m.CreateComputeNode(ctx, drained); err != nil {
		t.Fatalf("CreateComputeNode(drained): %v", err)
	}

	// ActiveComputeNodes should return both seeded default-local AND
	// the new active node, but skip drained. Result is sorted by name.
	nodes, err := m.ActiveComputeNodes(ctx)
	if err != nil {
		t.Fatalf("ActiveComputeNodes: %v", err)
	}
	gotNames := make([]string, 0, len(nodes))
	for _, n := range nodes {
		gotNames = append(gotNames, n.Name)
	}
	wantNames := []string{"active-node", DefaultLocalNodeName} // alphabetical
	if len(gotNames) != len(wantNames) {
		t.Fatalf("ActiveComputeNodes returned %d nodes, want %d (got=%v)", len(gotNames), len(wantNames), gotNames)
	}
	for i := range wantNames {
		if gotNames[i] != wantNames[i] {
			t.Errorf("ActiveComputeNodes[%d]=%q, want %q", i, gotNames[i], wantNames[i])
		}
	}
}

func TestMem_ComputeNodes_ComputeNodeByID_NotFound(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	if _, err := m.ComputeNodeByID(ctx, "no-such-id"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ComputeNodeByID(unknown): want ErrNotFound, got %v", err)
	}
}

func TestMem_ComputeNodes_ComputeNodeByName_NotFound(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	if _, err := m.ComputeNodeByName(ctx, "no-such-name"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ComputeNodeByName(unknown): want ErrNotFound, got %v", err)
	}
}

func TestMem_ComputeNodes_Heartbeat_BumpsAndUnknownReturnsNotFound(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	// Heartbeat an unknown id → ErrNotFound.
	if err := m.HeartbeatComputeNode(ctx, "no-such-id"); !errors.Is(err, ErrNotFound) {
		t.Errorf("HeartbeatComputeNode(unknown): want ErrNotFound, got %v", err)
	}

	// Create a node, capture original heartbeat, sleep briefly, heartbeat
	// again, and assert last_heartbeat_at moved forward.
	//
	// Flake guard: time.Now() is monotonic-resolution on most platforms
	// but a 2 ms sleep + a same-microsecond stamp on the goroutine can
	// still collapse on a busy CI runner. Retry once with a longer
	// sleep before failing. Same pattern as the PgStore test.
	node := state_computeNodeFixture("hb-node", true)
	created, err := m.CreateComputeNode(ctx, node)
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	if !assertMemHeartbeatAdvanced(t, m, ctx, created.ID, created.LastHeartbeatAt, 2*time.Millisecond) {
		if !assertMemHeartbeatAdvanced(t, m, ctx, created.ID, created.LastHeartbeatAt, 10*time.Millisecond) {
			t.Errorf("HeartbeatComputeNode did not bump LastHeartbeatAt after 2 retries")
		}
	}
}

func assertMemHeartbeatAdvanced(t *testing.T, m *MemStore, ctx context.Context, id string, before time.Time, sleep time.Duration) bool {
	t.Helper()
	time.Sleep(sleep)
	if err := m.HeartbeatComputeNode(ctx, id); err != nil {
		t.Fatalf("HeartbeatComputeNode: %v", err)
		return false
	}
	after, err := m.ComputeNodeByID(ctx, id)
	if err != nil {
		t.Fatalf("ComputeNodeByID: %v", err)
		return false
	}
	return after.LastHeartbeatAt.After(before)
}

func TestMem_ComputeNodes_CreateComputeNode_AutoFillsIDAndTimestamps(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	// Caller omits ID, CreatedAt, LastHeartbeatAt — MemStore fills them.
	in := state_computeNodeFixture("autofill", true)
	in.ID = ""
	in.CreatedAt = time.Time{}
	in.LastHeartbeatAt = time.Time{}

	got, err := m.CreateComputeNode(ctx, in)
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	if got.ID == "" {
		t.Errorf("MemStore should auto-fill ID")
	}
	if got.CreatedAt.IsZero() {
		t.Errorf("MemStore should stamp CreatedAt")
	}
	if got.LastHeartbeatAt.IsZero() {
		t.Errorf("MemStore should stamp LastHeartbeatAt (= CreatedAt when unset)")
	}
}

func TestMem_ComputeNodes_CreateComputeNode_DuplicateNameIsConflict(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	// 'default-local' is seeded on NewMemStore(); a second row with the
	// same name must ErrConflict, not overwrite the seeded row.
	dup := state_computeNodeFixture(DefaultLocalNodeName, true)
	if _, err := m.CreateComputeNode(ctx, dup); !errors.Is(err, ErrConflict) {
		t.Errorf("CreateComputeNode(duplicate name): want ErrConflict, got %v", err)
	}
}

func TestMem_ComputeNodes_UsedMB_SumsLiveInstancesOnly(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	// Create app + deployment to anchor instance rows. MemStore does
	// NOT enforce FK to compute_nodes (see memstore.go:1089) so we can
	// create instances on any nodeID — useful for negative-coverage tests.
	acct, err := m.CreateAccount(ctx, "u@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "node-mb", RAMMB: 256, MaxConcurrency: 4, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := m.CreateDeployment(ctx, Deployment{
		AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:k",
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	nodeA := "node-A"
	nodeB := "node-B"

	// nodeA: 2 waking, 1 cold_booting, 1 running, 1 stopped (not counted),
	// 1 snapshotted (not counted). Total live = 4 × (256 + 8) = 1056 MB.
	for _, st := range []string{"waking", "cold_booting", "running"} {
		if _, err := m.CreateInstance(ctx, app.ID, dep.ID, st, 256, nodeA, ""); err != nil {
			t.Fatalf("CreateInstance(%s): %v", st, err)
		}
	}
	if _, err := m.CreateInstance(ctx, app.ID, dep.ID, "running", 256, nodeA, ""); err != nil {
		t.Fatalf("CreateInstance(running-2): %v", err)
	}
	if _, err := m.CreateInstance(ctx, app.ID, dep.ID, "stopped", 256, nodeA, ""); err != nil {
		t.Fatalf("CreateInstance(stopped): %v", err)
	}
	if _, err := m.CreateInstance(ctx, app.ID, dep.ID, "snapshotted", 256, nodeA, ""); err != nil {
		t.Fatalf("CreateInstance(snapshotted): %v", err)
	}

	// nodeB: 1 running 512 MB → 520 MB total.
	if _, err := m.CreateInstance(ctx, app.ID, dep.ID, "running", 512, nodeB, ""); err != nil {
		t.Fatalf("CreateInstance(nodeB): %v", err)
	}

	gotA, err := m.ComputeNodeUsedMB(ctx, nodeA)
	if err != nil {
		t.Fatalf("ComputeNodeUsedMB(nodeA): %v", err)
	}
	wantA := int64(4 * (256 + api.PerVMOverheadMB))
	if gotA != wantA {
		t.Errorf("ComputeNodeUsedMB(nodeA)=%d, want %d (4 live × (256+8))", gotA, wantA)
	}

	gotB, err := m.ComputeNodeUsedMB(ctx, nodeB)
	if err != nil {
		t.Fatalf("ComputeNodeUsedMB(nodeB): %v", err)
	}
	wantB := int64(512 + api.PerVMOverheadMB)
	if gotB != wantB {
		t.Errorf("ComputeNodeUsedMB(nodeB)=%d, want %d", gotB, wantB)
	}

	// Unknown node → 0 (no error).
	gotU, err := m.ComputeNodeUsedMB(ctx, "no-such-node")
	if err != nil {
		t.Fatalf("ComputeNodeUsedMB(unknown): %v", err)
	}
	if gotU != 0 {
		t.Errorf("ComputeNodeUsedMB(unknown)=%d, want 0", gotU)
	}
}

// state_computeNodeFixture builds a valid ComputeNode for tests — a
// fresh node with a unique name and the production-shape field set.
// All non-name fields use the same values the production default-local
// row carries, so positive tests assert against a known shape.
func state_computeNodeFixture(name string, active bool) ComputeNode {
	return ComputeNode{
		Name:               name,
		TargetURL:          "unix:///run/faas/vmmd.sock",
		VPCPUs:             160,
		MemMB:              56000,
		MaxConcurrency:     200,
		AdmissionCeilingMB: 47600,
		Active:             active,
	}
}

func TestMemStore_SnapshotStorageKey_RoundTrip(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, _ := m.CreateAccount(ctx, "u@example.com", "pro")
	app, _ := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "snap-key", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 1,
	})
	dep, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:k"})

	// (1) Caller-supplied storage_key round-trips through CreateSnapshot → LatestSnapshot.
	want := "snap/" + dep.ID + "/mem"
	snap, err := m.CreateSnapshot(ctx, Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.8.0", MemBytes: 100, DiskBytes: 100,
		StorageKey: want,
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if snap.StorageKey != want {
		t.Errorf("CreateSnapshot returned StorageKey=%q, want %q", snap.StorageKey, want)
	}
	got, err := m.LatestSnapshot(ctx, dep.ID)
	if err != nil {
		t.Fatalf("LatestSnapshot: %v", err)
	}
	if got.StorageKey != want {
		t.Errorf("LatestSnapshot returned StorageKey=%q, want %q", got.StorageKey, want)
	}

	// (2) ListSnapshotsForGC exposes the same value on SnapshotForGC
	// so imaged's GC loop can Storage.Delete under the canonical key.
	rows, err := m.ListSnapshotsForGC(ctx)
	if err != nil {
		t.Fatalf("ListSnapshotsForGC: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListSnapshotsForGC returned %d rows, want 1", len(rows))
	}
	if rows[0].StorageKey != want {
		t.Errorf("SnapshotForGC.StorageKey = %q, want %q", rows[0].StorageKey, want)
	}
}

// TestMemStore_ListStaleQueuedBuilds covers the imaged-reaper
// surface (PR-A). Two queued builds — one old (5min), one fresh
// (now) — plus one build already in BuildRunning. The threshold
// (1min) should filter to just the old queued row. The non-queued
// row must not appear regardless of age. MemStore keeps EnqueuedAt
// private so we mutate the map directly under m.mu.
func TestMemStore_ListStaleQueuedBuilds(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "reap@example.com", "pro")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "reap-app", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	oldDep, _ := m.CreateDeployment(ctx, Deployment{
		AppID: app.ID, Kind: DeploymentKindTarball, Status: DeployPending,
	})
	freshDep, _ := m.CreateDeployment(ctx, Deployment{
		AppID: app.ID, Kind: DeploymentKindTarball, Status: DeployPending,
	})
	runningDep, _ := m.CreateDeployment(ctx, Deployment{
		AppID: app.ID, Kind: DeploymentKindTarball, Status: DeployBuilding,
	})

	oldBuild, err := m.CreateBuild(ctx, oldDep.ID, DeploymentKindTarball, 100, "")
	if err != nil {
		t.Fatalf("CreateBuild old: %v", err)
	}
	freshBuild, err := m.CreateBuild(ctx, freshDep.ID, DeploymentKindTarball, 100, "")
	if err != nil {
		t.Fatalf("CreateBuild fresh: %v", err)
	}
	runningBuild, err := m.CreateBuild(ctx, runningDep.ID, DeploymentKindTarball, 100, "")
	if err != nil {
		t.Fatalf("CreateBuild running: %v", err)
	}

	// Backdate the old build's EnqueuedAt 5min into the past; flip
	// the running build to BuildRunning + backdate similarly (it
	// must NOT appear regardless of age).
	m.mu.Lock()
	old := m.builds[oldBuild.ID]
	old.EnqueuedAt = time.Now().Add(-5 * time.Minute)
	m.builds[oldBuild.ID] = old
	run := m.builds[runningBuild.ID]
	run.Status = BuildRunning
	run.EnqueuedAt = time.Now().Add(-5 * time.Minute)
	m.builds[runningBuild.ID] = run
	m.mu.Unlock()

	// Threshold = 1 minute: only the backdated queued row qualifies.
	out, err := m.ListStaleQueuedBuilds(ctx, time.Minute)
	if err != nil {
		t.Fatalf("ListStaleQueuedBuilds: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d stale builds, want 1", len(out))
	}
	if out[0].ID != oldBuild.ID {
		t.Errorf("stale id = %q, want %q", out[0].ID, oldBuild.ID)
	}
	if out[0].ID == freshBuild.ID {
		t.Errorf("fresh build leaked into stale list")
	}
	if out[0].ID == runningBuild.ID {
		t.Errorf("non-queued build leaked into stale list")
	}

	// Threshold = 0 → the predicate becomes `enqueued_at < now()`. Both
	// queued rows have EnqueuedAt <= now, so both qualify. This pins the
	// reaper's "threshold is inclusive of zero" semantics — a caller that
	// wants to suppress every row should pass time.Duration(-1) (which
	// flips the predicate to `enqueued_at > now()` → none). Mirrors the
	// PgStore behaviour exactly so the two impls stay in lockstep.
	out2, err := m.ListStaleQueuedBuilds(ctx, 0)
	if err != nil {
		t.Fatalf("ListStaleQueuedBuilds(0): %v", err)
	}
	if len(out2) != 2 {
		t.Errorf("threshold=0 returned %d rows, want 2 (predicate is `enqueued_at < now()`)", len(out2))
	}
}

// TestMemStore_ClaimQueuedBuild pins the atomic queued → running CAS
// that closes the apid/reaper double-emit race (PR-A review). First
// claim wins; subsequent claims return ErrNotFound. Mirrors
// TestPg_ClaimQueuedBuild so the two backends stay in lock-step.
func TestMemStore_ClaimQueuedBuild(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "claim@example.com", "pro")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "claim-app", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := m.CreateDeployment(ctx, Deployment{
		AppID: app.ID, Kind: DeploymentKindTarball, Status: DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	b, err := m.CreateBuild(ctx, dep.ID, DeploymentKindTarball, 100, "")
	if err != nil {
		t.Fatalf("CreateBuild: %v", err)
	}

	// First claim wins.
	won, err := m.ClaimQueuedBuild(ctx, b.ID)
	if err != nil {
		t.Fatalf("first ClaimQueuedBuild: %v", err)
	}
	if won.Status != BuildRunning {
		t.Errorf("first claim status = %q, want running", won.Status)
	}
	if won.StartedAt.IsZero() {
		t.Errorf("first claim started_at is zero")
	}

	// Second claim loses — row is no longer queued.
	_, err = m.ClaimQueuedBuild(ctx, b.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("second claim err = %v, want ErrNotFound", err)
	}

	// Unknown id loses the same way.
	_, err = m.ClaimQueuedBuild(ctx, "deadbeef")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id err = %v, want ErrNotFound", err)
	}
}

// TestMemStore_ListLatestInstancesForApp_BoundedLimit asserts the
// dashboard's "Recent wakes" path returns at most `limit` rows and
// that limit ≤ 0 fails closed (empty slice, not unbounded). Added
// alongside the bounded SQL path for the dashboard per gaps analysis
// 2026-07-23 review finding #5 — the previous Go-side sort on
// ListInstancesForApp was unbounded for long-lived apps.
func TestMemStore_ListLatestInstancesForApp_BoundedLimit(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "listlim@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "listlim", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := m.CreateDeployment(ctx, Deployment{
		AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:abc", Status: DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := m.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}

	// Seed 5 instances for this app — they all share the same
	// started_at second so the sort order between them is stable
	// (sorted by StartedAt after write); the bounded path must
	// still return exactly `limit` rows.
	for i := 0; i < 5; i++ {
		_, err := m.CreateInstance(ctx, app.ID, dep.ID, "parked", 256, DefaultLocalNodeName, "")
		if err != nil {
			t.Fatalf("CreateInstance %d: %v", i, err)
		}
	}

	// limit=3 returns exactly 3 rows.
	rows, err := m.ListLatestInstancesForApp(ctx, app.ID, 3)
	if err != nil {
		t.Fatalf("ListLatestInstancesForApp(3): %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("rows = %d, want 3 (limit bound)", len(rows))
	}

	// limit=0 and limit=-1 fail closed.
	for _, lim := range []int{0, -1} {
		rows, err := m.ListLatestInstancesForApp(ctx, app.ID, lim)
		if err != nil {
			t.Fatalf("ListLatestInstancesForApp(%d): %v", lim, err)
		}
		if len(rows) != 0 {
			t.Errorf("limit=%d returned %d rows, want 0 (fail-closed)", lim, len(rows))
		}
	}

	// limit larger than the row count returns everything, no error.
	rows, err = m.ListLatestInstancesForApp(ctx, app.ID, 50)
	if err != nil {
		t.Fatalf("ListLatestInstancesForApp(50): %v", err)
	}
	if len(rows) != 5 {
		t.Errorf("rows = %d, want 5 (all)", len(rows))
	}
}
