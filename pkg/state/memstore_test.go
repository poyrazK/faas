package state

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

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
