// adr: 20261116213000 — custom Postgres group roles.
package managedpostgres

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

type customRoleProvider struct {
	bindingProvider
	applied  [][]CustomRoleSpec
	applyErr error
}

func (p *customRoleProvider) ApplyCustomRoles(_ context.Context, _ string, desired []CustomRoleSpec) error {
	p.applied = append(p.applied, desired)
	return p.applyErr
}

type customRoleFixture struct {
	provider *customRoleProvider
	store    *MemoryStore
	roles    *CustomRoleService
	bindings *BindingService
	database Database
	now      time.Time
}

func newCustomRoleServiceFixture(t *testing.T) *customRoleFixture {
	t.Helper()
	f := &customRoleFixture{provider: &customRoleProvider{}, store: NewMemoryStore(), now: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	f.provider.capabilities = testCapabilities()
	f.provider.capabilities.CredentialAccess = []CredentialAccess{CredentialReadWrite, CredentialReadOnly, CredentialMigration, CredentialDataAPI}
	f.provider.provisionStatus = ProviderStatusReady
	f.provider.material = bindingTestMaterial()
	registry := testRegistry(t, f.provider, func(config *Config) { config.ProvisioningEnabled = true })
	now := func() time.Time { return f.now }
	databases, err := NewService(registry, f.store, ServiceOptions{
		LeaseDuration: time.Minute, ProviderTimeout: time.Second, ProvisioningEnabled: func() bool { return true },
		Now: now, NewID: func() string { return "database-roles" }, NewLeaseToken: func() string { return "lease" },
	})
	if err != nil {
		t.Fatal(err)
	}
	f.database, err = databases.Create(context.Background(), CreateRequest{AccountID: "account-a", Name: "windmill-db", Spec: testSpec()})
	if err != nil || f.database.State != StateReady {
		t.Fatalf("create database: %+v %v", f.database, err)
	}
	ids := 0
	f.bindings, err = NewBindingService(registry, f.store, f.store, newBindingCredentialSink(), BindingServiceOptions{
		LeaseDuration: time.Minute, ProviderTimeout: time.Second, ProvisioningEnabled: func() bool { return true },
		Now: now, NewID: func() string { ids++; return "binding-" + string(rune('a'+ids)) }, NewLeaseToken: func() string { return "binding-lease" },
	})
	if err != nil {
		t.Fatal(err)
	}
	f.roles, err = f.bindings.CustomRoles()
	if err != nil {
		t.Fatal(err)
	}
	f.roles.now = now
	return f
}

func (f *customRoleFixture) save(name string, bypass bool, grants []CredentialAccess, memberOf ...string) (CustomRole, error) {
	return f.roles.Save(context.Background(), SaveCustomRoleRequest{AccountID: "account-a", DatabaseID: f.database.ID, Name: name, BypassRLS: bypass, GrantedTo: grants, MemberOf: memberOf})
}

func TestCustomRoleServiceDeclaresAppliesAndFences(t *testing.T) {
	f := newCustomRoleServiceFixture(t)
	ctx := context.Background()
	both := []CredentialAccess{CredentialReadWrite, CredentialMigration}

	user, err := f.save("windmill_user", false, both)
	if err != nil || user.State != CustomRoleReady {
		t.Fatalf("save windmill_user: %+v %v", user, err)
	}
	admin, err := f.save("windmill_admin", true, append(both, CredentialSession), "windmill_user")
	if err != nil || admin.State != CustomRoleReady || !admin.BypassRLS {
		t.Fatalf("save windmill_admin: %+v %v", admin, err)
	}
	if !slices.Equal(admin.GrantedTo, []CredentialAccess{CredentialMigration, CredentialReadWrite}) {
		t.Fatalf("grants not normalized: %v", admin.GrantedTo)
	}
	last := f.provider.applied[len(f.provider.applied)-1]
	if len(last) != 2 || last[0].Name != "windmill_user" || last[1].Name != "windmill_admin" {
		t.Fatalf("desired roles not ordered parents first: %+v", last)
	}

	// Refusals.
	for name, err := range map[string]error{
		"reserved prefix": func() error { _, err := f.save("gregale_admin", false, nil); return err }(),
		"bad name":        func() error { _, err := f.save("Windmill", false, nil); return err }(),
		"data api grant":  func() error { _, err := f.save("other", false, []CredentialAccess{CredentialDataAPI}); return err }(),
		"missing parent":  func() error { _, err := f.save("other", false, nil, "nobody"); return err }(),
		"cycle":           func() error { _, err := f.save("windmill_user", false, both, "windmill_admin"); return err }(),
	} {
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}

	// A login issued later receives the roles granted to its access mode.
	if _, err := f.bindings.Create(ctx, CreateBindingRequest{AccountID: "account-a", DatabaseID: f.database.ID, AppID: "app-a",
		Scope: "production", EnvironmentKey: "DATABASE_URL", Access: CredentialReadWrite}); err != nil {
		t.Fatal(err)
	}
	if got := f.provider.lastIssueRequest.CustomRoles.Names(); !slices.Equal(got, []string{"windmill_admin", "windmill_user"}) {
		t.Fatalf("read_write login custom roles = %v", got)
	}
	if _, err := f.bindings.Create(ctx, CreateBindingRequest{AccountID: "account-a", DatabaseID: f.database.ID, AppID: "app-a",
		Scope: "production", EnvironmentKey: "READ_URL", Access: CredentialReadOnly}); err != nil {
		t.Fatal(err)
	}
	if got := f.provider.lastIssueRequest.CustomRoles; got != "" {
		t.Fatalf("read_only login got custom roles %v", got)
	}

	// Data API relies on RLS: refused while a BYPASSRLS role exists.
	if _, err := f.bindings.Create(ctx, CreateBindingRequest{AccountID: "account-a", DatabaseID: f.database.ID, AppID: "app-a",
		Scope: "production", EnvironmentKey: "DATA_API_URL", Access: CredentialDataAPI}); !errors.Is(err, ErrBypassRLSConflict) {
		t.Fatalf("data api binding with a BYPASSRLS role: %v", err)
	}

	// A parent role cannot be deleted while another role is its member.
	if _, err := f.roles.Delete(ctx, "account-a", f.database.ID, "windmill_user"); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete parent: %v", err)
	}
	deleted, err := f.roles.Delete(ctx, "account-a", f.database.ID, "windmill_admin")
	if err != nil || deleted.State != CustomRoleDeleted {
		t.Fatalf("delete admin: %+v %v", deleted, err)
	}
	// With no BYPASSRLS role left, Data API binds; then BYPASSRLS is refused.
	if _, err := f.bindings.Create(ctx, CreateBindingRequest{AccountID: "account-a", DatabaseID: f.database.ID, AppID: "app-a",
		Scope: "production", EnvironmentKey: "DATA_API_URL", Access: CredentialDataAPI}); err != nil {
		t.Fatalf("data api binding without BYPASSRLS roles: %v", err)
	}
	if _, err := f.save("windmill_admin", true, both, "windmill_user"); !errors.Is(err, ErrBypassRLSConflict) {
		t.Fatalf("BYPASSRLS role on a Data API database: %v", err)
	}
	roles, err := f.roles.List(ctx, "account-a", f.database.ID)
	if err != nil || len(roles) != 1 || roles[0].Name != "windmill_user" {
		t.Fatalf("roles after delete: %+v %v", roles, err)
	}
	// Another account sees nothing.
	if _, err := f.roles.List(ctx, "account-b", f.database.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other account list: %v", err)
	}
}

func TestCustomRoleServiceRetriesFailedApplies(t *testing.T) {
	f := newCustomRoleServiceFixture(t)
	ctx := context.Background()
	f.provider.applyErr = ErrUnavailable
	role, err := f.save("app_reader", false, []CredentialAccess{CredentialReadOnly})
	if err != nil || role.State != CustomRolePending || role.LastError != "unavailable" {
		t.Fatalf("failed apply: %+v %v", role, err)
	}
	// Not yet due.
	if err := f.roles.Sweep(ctx, 10); err != nil {
		t.Fatal(err)
	}
	calls := len(f.provider.applied)
	f.provider.applyErr = nil
	f.now = f.now.Add(time.Hour)
	if err := f.roles.Sweep(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if len(f.provider.applied) != calls+1 {
		t.Fatalf("sweep applied %d times, want 1", len(f.provider.applied)-calls)
	}
	roles, err := f.roles.List(ctx, "account-a", f.database.ID)
	if err != nil || len(roles) != 1 || roles[0].State != CustomRoleReady || roles[0].LastError != "" {
		t.Fatalf("after sweep: %+v %v", roles, err)
	}
	// A provider refusal of BYPASSRLS is returned, not retried silently.
	f.provider.applyErr = ErrBypassRLSUnavailable
	if _, err := f.save("app_admin", true, nil); !errors.Is(err, ErrBypassRLSUnavailable) {
		t.Fatalf("bypass unavailable: %v", err)
	}
}

func TestCustomRoleServiceLimitsRolesPerDatabase(t *testing.T) {
	f := newCustomRoleServiceFixture(t)
	for i := range CustomRolesMax {
		if _, err := f.save("role_"+string(rune('a'+i)), false, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.save("one_too_many", false, nil); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("17th role: %v", err)
	}
	// Replacing an existing role is not a new one.
	if _, err := f.save("role_a", false, []CredentialAccess{CredentialMigration}); err != nil {
		t.Fatal(err)
	}
}

// TestCustomRoleServiceDefaultFor covers amendment 2's rules: a default must
// be granted, one per access mode, kept when a request leaves it out,
// trimmed when its grant is revoked, and handed to logins issued later.
func TestCustomRoleServiceDefaultFor(t *testing.T) {
	f := newCustomRoleServiceFixture(t)
	ctx := context.Background()
	both := []CredentialAccess{CredentialMigration, CredentialReadWrite}
	save := func(name string, grants []CredentialAccess, defaults *[]CredentialAccess) (CustomRole, error) {
		return f.roles.Save(ctx, SaveCustomRoleRequest{AccountID: "account-a", DatabaseID: f.database.ID, Name: name,
			BypassRLS: name == "windmill_admin", GrantedTo: grants, DefaultFor: defaults})
	}
	readWrite := []CredentialAccess{CredentialSession}
	admin, err := save("windmill_admin", both, &readWrite)
	if err != nil || !slices.Equal(admin.DefaultFor, []CredentialAccess{CredentialReadWrite}) {
		t.Fatalf("save default: %+v %v", admin, err)
	}
	if last := f.provider.applied[len(f.provider.applied)-1]; !slices.Equal(last[0].DefaultFor, []CredentialAccess{CredentialReadWrite}) {
		t.Fatalf("provider spec default = %v", last[0].DefaultFor)
	}
	for name, defaults := range map[string][]CredentialAccess{
		"migration":   {CredentialMigration},
		"not granted": {CredentialReadOnly},
		"data api":    {CredentialDataAPI},
	} {
		if _, err := save("windmill_admin", both, &defaults); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s default: %v, want ErrInvalid", name, err)
		}
	}
	if _, err := save("windmill_user", both, &readWrite); !errors.Is(err, ErrCustomRoleDefaultTaken) {
		t.Fatalf("second read_write default: %v", err)
	}
	// Leaving default_for out (an older client's grant) keeps it.
	kept, err := save("windmill_admin", append(slices.Clone(both), CredentialReadOnly), nil)
	if err != nil || !slices.Equal(kept.DefaultFor, []CredentialAccess{CredentialReadWrite}) {
		t.Fatalf("omitted default_for: %+v %v", kept, err)
	}
	// A login issued later starts as the default.
	if _, err := f.bindings.Create(ctx, CreateBindingRequest{AccountID: "account-a", DatabaseID: f.database.ID, AppID: "app-a",
		Scope: "production", EnvironmentKey: "DATABASE_URL", Access: CredentialReadWrite}); err != nil {
		t.Fatal(err)
	}
	if got := f.provider.lastIssueRequest.CustomRoleDefault; got != "windmill_admin" {
		t.Fatalf("read_write login default = %q", got)
	}
	if _, err := f.bindings.Create(ctx, CreateBindingRequest{AccountID: "account-a", DatabaseID: f.database.ID, AppID: "app-a",
		Scope: "production", EnvironmentKey: "READ_URL", Access: CredentialReadOnly}); err != nil {
		t.Fatal(err)
	}
	if got := f.provider.lastIssueRequest.CustomRoleDefault; got != "" {
		t.Fatalf("read_only login default = %q", got)
	}
	// Revoking read_write drops the default with it.
	trimmed, err := save("windmill_admin", []CredentialAccess{CredentialMigration}, nil)
	if err != nil || len(trimmed.DefaultFor) != 0 {
		t.Fatalf("revoked grant kept its default: %+v %v", trimmed, err)
	}
	// Now another role may take it.
	if _, err := save("windmill_user", both, &readWrite); err != nil {
		t.Fatalf("default after the other was revoked: %v", err)
	}
}

func TestValidateCustomRoleName(t *testing.T) {
	for _, name := range []string{"windmill_user", "_app", "a", "app_role_2"} {
		if err := ValidateCustomRoleName(name); err != nil {
			t.Errorf("%q refused: %v", name, err)
		}
	}
	for _, name := range []string{"", "Admin", "1role", "pg_monitor", "gregale_x", "neon_superuser", "public", "postgres", "role-x", "a b", "rds_admin"} {
		if err := ValidateCustomRoleName(name); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q accepted", name)
		}
	}
}
