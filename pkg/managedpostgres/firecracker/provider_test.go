// adr: 1027
package firecracker

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha256"
	"errors"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/pgscram"
)

const testDatabaseID = "0b7d3c5e-8f43-4c7a-9d0e-5a1f2b3c4d5e"

// fakeScheduler is schedd's database scheduler: one VM per id.
type fakeScheduler struct {
	mu        sync.Mutex
	vms       map[string]*scheddpb.DatabaseRuntime
	provision []*scheddpb.ProvisionDatabaseRequest
	admin     []string
	// adminRun, when set, executes admin batches (the pg test runs them
	// through psql); otherwise they succeed.
	adminRun     func(sql string) (*scheddpb.DatabaseAdminResponse, error)
	provisionErr error
	deleteErr    error
	failVM       bool
}

func newFakeScheduler() *fakeScheduler {
	return &fakeScheduler{vms: map[string]*scheddpb.DatabaseRuntime{}}
}

func (f *fakeScheduler) ProvisionDatabase(_ context.Context, in *scheddpb.ProvisionDatabaseRequest, _ ...grpc.CallOption) (*scheddpb.DatabaseRuntime, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.provision = append(f.provision, in)
	if f.provisionErr != nil {
		return nil, f.provisionErr
	}
	state := "parked"
	if f.failVM {
		state = "failed"
	}
	rt := &scheddpb.DatabaseRuntime{DatabaseId: in.GetDatabaseId(), State: state, PostgresMajor: in.GetPostgresMajor(),
		VcpuCount: in.GetVcpuCount(), MemMib: in.GetMemMib(), VolumeBytes: in.GetVolumeBytes()}
	f.vms[in.GetDatabaseId()] = rt
	return rt, nil
}

func (f *fakeScheduler) GetDatabase(_ context.Context, in *scheddpb.DatabaseRef, _ ...grpc.CallOption) (*scheddpb.DatabaseRuntime, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rt, ok := f.vms[in.GetDatabaseId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "dbsched: database not found")
	}
	return rt, nil
}

func (f *fakeScheduler) DatabaseAdmin(_ context.Context, in *scheddpb.DatabaseAdminRequest, _ ...grpc.CallOption) (*scheddpb.DatabaseAdminResponse, error) {
	f.mu.Lock()
	_, ok := f.vms[in.GetDatabaseId()]
	f.admin = append(f.admin, in.GetSql())
	run := f.adminRun
	f.mu.Unlock()
	if !ok {
		return nil, status.Error(codes.NotFound, "dbsched: database not found")
	}
	if run != nil {
		return run(in.GetSql())
	}
	return &scheddpb.DatabaseAdminResponse{}, nil
}

func (f *fakeScheduler) DeleteDatabase(_ context.Context, in *scheddpb.DatabaseRef, _ ...grpc.CallOption) (*scheddpb.DeleteDatabaseResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	if _, ok := f.vms[in.GetDatabaseId()]; !ok {
		return nil, status.Error(codes.NotFound, "dbsched: database not found")
	}
	delete(f.vms, in.GetDatabaseId())
	return &scheddpb.DeleteDatabaseResponse{}, nil
}

func (f *fakeScheduler) lastAdmin() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.admin) == 0 {
		return ""
	}
	return f.admin[len(f.admin)-1]
}

// fakeVerifiers is the verifier table pgproxyd reads.
type fakeVerifiers struct {
	mu   sync.Mutex
	byID map[string]map[string]pgscram.Verifier
	// ops records Put/Delete order against admin batches.
	ops    []string
	putErr error
}

func newFakeVerifiers() *fakeVerifiers {
	return &fakeVerifiers{byID: map[string]map[string]pgscram.Verifier{}}
}

func (f *fakeVerifiers) Put(_ context.Context, databaseID, role string, v pgscram.Verifier) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops = append(f.ops, "put "+role)
	if f.putErr != nil {
		return f.putErr
	}
	if f.byID[databaseID] == nil {
		f.byID[databaseID] = map[string]pgscram.Verifier{}
	}
	f.byID[databaseID][role] = v
	return nil
}

func (f *fakeVerifiers) Delete(_ context.Context, databaseID, role string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops = append(f.ops, "delete "+role)
	delete(f.byID[databaseID], role)
	return nil
}

func (f *fakeVerifiers) DeleteDatabase(_ context.Context, databaseID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops = append(f.ops, "delete-database")
	delete(f.byID, databaseID)
	return nil
}

func (f *fakeVerifiers) get(databaseID, role string) (pgscram.Verifier, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.byID[databaseID][role]
	return v, ok
}

func testConfig(t *testing.T) Config {
	t.Helper()
	cfg, err := ParseConfig(managedpostgres.BackendConfig{Region: "eu-central", Settings: map[string]string{settingHostSuffix: "db.gregale.test"}})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func newTestProvider(t *testing.T) (*Provider, *fakeScheduler, *fakeVerifiers) {
	t.Helper()
	sched, verifiers := newFakeScheduler(), newFakeVerifiers()
	p, err := New(testConfig(t), sched, verifiers)
	if err != nil {
		t.Fatal(err)
	}
	return p, sched, verifiers
}

func devSpec() managedpostgres.Spec {
	return managedpostgres.Spec{Region: "eu-central", PostgresMajor: 17, Class: managedpostgres.ClassDevelopment,
		Availability: managedpostgres.AvailabilitySingleZone, ScaleToZero: true, StorageLimitBytes: 1 << 30}
}

// passwordMatches checks a password against a verifier the way PostgreSQL
// does (RFC 5802 StoredKey).
func passwordMatches(t *testing.T, v pgscram.Verifier, password string) bool {
	t.Helper()
	salted, err := pbkdf2.Key(sha256.New, password, v.Salt, v.Iterations, sha256.Size)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, salted)
	mac.Write([]byte("Client Key"))
	stored := sha256.Sum256(mac.Sum(nil))
	return bytes.Equal(stored[:], v.StoredKey)
}

func TestParseConfig(t *testing.T) {
	base := func(kv ...string) managedpostgres.BackendConfig {
		s := map[string]string{settingHostSuffix: ".DB.Gregale.Test"}
		for i := 0; i+1 < len(kv); i += 2 {
			s[kv[i]] = kv[i+1]
		}
		return managedpostgres.BackendConfig{Region: "eu-central", Settings: s}
	}
	cfg, err := ParseConfig(base())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HostSuffix != "db.gregale.test" || cfg.Port != 5432 || cfg.TLSMode != "verify-full" || cfg.Database != "gregale" ||
		len(cfg.Majors) != 1 || cfg.Majors[0] != 17 || cfg.MaxStorageBytes != 100<<30 {
		t.Fatalf("defaults = %+v", cfg)
	}
	cfg, err = ParseConfig(base(settingPort, "6432", settingTLSMode, "require", settingDatabase, "app_db", settingMajors, "16, 17", settingMaxStorage, "2147483648"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 6432 || cfg.TLSMode != "require" || cfg.Database != "app_db" || len(cfg.Majors) != 2 || cfg.MaxStorageBytes != 2<<30 {
		t.Fatalf("overrides = %+v", cfg)
	}

	invalid := []struct {
		name string
		cfg  managedpostgres.BackendConfig
	}{
		{"no host suffix", managedpostgres.BackendConfig{Settings: map[string]string{}}},
		{"port zero", base(settingPort, "0")},
		{"port overflow", base(settingPort, "70000")},
		{"tls disable", base(settingTLSMode, "disable")},
		{"database postgres", base(settingDatabase, "postgres")},
		{"database uppercase", base(settingDatabase, "App")},
		{"database injection", base(settingDatabase, "x; drop")},
		{"major too old", base(settingMajors, "13")},
		{"major not a number", base(settingMajors, "17,x")},
		{"storage under 1 GiB", base(settingMaxStorage, "1048576")},
	}
	for _, tc := range invalid {
		if _, err := ParseConfig(tc.cfg); err == nil {
			t.Errorf("%s: accepted", tc.name)
		}
	}
}

func TestCapabilitiesAreValidAndHonest(t *testing.T) {
	p, _, _ := newTestProvider(t)
	caps := p.Capabilities()
	if err := caps.Validate(); err != nil {
		t.Fatalf("capabilities invalid: %v", err)
	}
	if caps.PointInTimeRestore || len(caps.UsageMeters) != 0 || !caps.ScaleToZero || caps.ScaleToZeroUpdate || caps.ClassResize {
		t.Fatalf("capabilities claim more than the backend does: %+v", caps)
	}
	if err := caps.Supports(devSpec()); err != nil {
		t.Fatalf("dev spec unsupported: %v", err)
	}
	withPITR := devSpec()
	withPITR.RestoreWindowSeconds = 86400
	if err := caps.Supports(withPITR); !errors.Is(err, managedpostgres.ErrUnsupported) {
		t.Fatalf("restore window admitted: %v", err)
	}
}

func TestProvisionCreatesVMAndDatabase(t *testing.T) {
	p, sched, _ := newTestProvider(t)
	ctx := context.Background()
	spec := devSpec()
	obs, err := p.Provision(ctx, managedpostgres.ProvisionRequest{ResourceID: testDatabaseID, Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	if obs.ProviderResourceID != testDatabaseID || obs.Status != managedpostgres.ProviderStatusReady || obs.ComputeState != managedpostgres.ComputeStateSuspended {
		t.Fatalf("observed = %+v", obs)
	}
	if obs.Spec != spec {
		t.Fatalf("observed spec %+v != requested %+v; the service would fail it as spec_mismatch", obs.Spec, spec)
	}
	req := sched.provision[0]
	if req.GetVcpuCount() != 1 || req.GetMemMib() != 512 || req.GetVolumeBytes() != 1<<30 || req.GetPostgresMajor() != 17 {
		t.Fatalf("provision request = %+v", req)
	}
	if init := sched.lastAdmin(); !strings.Contains(init, "CREATE DATABASE gregale OWNER gregale_schema_owner") {
		t.Fatalf("init batch = %q", init)
	}

	burstable := spec
	burstable.Class = managedpostgres.ClassBurstable
	obs, err = p.Provision(ctx, managedpostgres.ProvisionRequest{ResourceID: "1b7d3c5e-8f43-4c7a-9d0e-5a1f2b3c4d5e", Spec: burstable})
	if err != nil || obs.Spec != burstable {
		t.Fatalf("burstable observed = %+v, %v", obs.Spec, err)
	}
}

func TestProvisionRejectsSpecsItCannotReportBack(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*managedpostgres.Spec)
		want   error
	}{
		{"always on", func(s *managedpostgres.Spec) { s.ScaleToZero = false }, managedpostgres.ErrUnsupported},
		{"restore window", func(s *managedpostgres.Spec) { s.RestoreWindowSeconds = 3600 }, managedpostgres.ErrUnsupported},
		{"other region", func(s *managedpostgres.Spec) { s.Region = "us-east" }, managedpostgres.ErrUnsupported},
		{"unbounded storage", func(s *managedpostgres.Spec) { s.StorageLimitBytes = 0 }, managedpostgres.ErrUnsupported},
		{"storage over max", func(s *managedpostgres.Spec) { s.StorageLimitBytes = 200 << 30 }, managedpostgres.ErrUnsupported},
		{"production class", func(s *managedpostgres.Spec) { s.Class = managedpostgres.ClassProduction }, managedpostgres.ErrUnsupported},
		{"major not offered", func(s *managedpostgres.Spec) { s.PostgresMajor = 16 }, managedpostgres.ErrUnsupported},
		{"highly available", func(s *managedpostgres.Spec) { s.Availability = managedpostgres.AvailabilityHighlyAvailable }, managedpostgres.ErrUnsupported},
		{"invalid class", func(s *managedpostgres.Spec) { s.Class = "huge" }, managedpostgres.ErrInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, sched, _ := newTestProvider(t)
			spec := devSpec()
			tc.mutate(&spec)
			_, err := p.Provision(context.Background(), managedpostgres.ProvisionRequest{ResourceID: testDatabaseID, Spec: spec})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if len(sched.provision) != 0 {
				t.Fatal("an unsupported spec reached schedd")
			}
		})
	}
}

func TestProvisionFailedVMSkipsInit(t *testing.T) {
	p, sched, _ := newTestProvider(t)
	sched.failVM = true
	obs, err := p.Provision(context.Background(), managedpostgres.ProvisionRequest{ResourceID: testDatabaseID, Spec: devSpec()})
	if err != nil || obs.Status != managedpostgres.ProviderStatusFailed {
		t.Fatalf("observed = %+v, %v", obs, err)
	}
	if len(sched.admin) != 0 {
		t.Fatal("init batch ran on a failed VM")
	}
}

func TestObservedStates(t *testing.T) {
	cases := map[string]struct {
		status  managedpostgres.ProviderStatus
		compute managedpostgres.ComputeState
	}{
		"provisioning": {managedpostgres.ProviderStatusPending, managedpostgres.ComputeStateUnknown},
		"parked":       {managedpostgres.ProviderStatusReady, managedpostgres.ComputeStateSuspended},
		"waking":       {managedpostgres.ProviderStatusReady, managedpostgres.ComputeStateWaking},
		"running":      {managedpostgres.ProviderStatusReady, managedpostgres.ComputeStateActive},
		"parking":      {managedpostgres.ProviderStatusReady, managedpostgres.ComputeStateWaking},
		"deleting":     {managedpostgres.ProviderStatusDeleting, managedpostgres.ComputeStateUnknown},
		"failed":       {managedpostgres.ProviderStatusFailed, managedpostgres.ComputeStateUnknown},
	}
	p, sched, _ := newTestProvider(t)
	for state, want := range cases {
		sched.vms[testDatabaseID] = &scheddpb.DatabaseRuntime{DatabaseId: testDatabaseID, State: state}
		obs, err := p.Inspect(context.Background(), testDatabaseID)
		if err != nil || obs.Status != want.status || obs.ComputeState != want.compute {
			t.Errorf("%s: observed %s/%s, %v; want %s/%s", state, obs.Status, obs.ComputeState, err, want.status, want.compute)
		}
	}
	if _, err := p.Inspect(context.Background(), "2b7d3c5e-8f43-4c7a-9d0e-5a1f2b3c4d5e"); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatalf("missing database = %v", err)
	}
}

func TestIssueCredentialsPublishesOnlyAVerifier(t *testing.T) {
	p, sched, verifiers := newTestProvider(t)
	ctx := context.Background()
	if _, err := p.Provision(ctx, managedpostgres.ProvisionRequest{ResourceID: testDatabaseID, Spec: devSpec()}); err != nil {
		t.Fatal(err)
	}
	for _, access := range []managedpostgres.CredentialAccess{managedpostgres.CredentialReadWrite, managedpostgres.CredentialReadOnly, managedpostgres.CredentialMigration} {
		req := managedpostgres.CredentialRequest{ProviderResourceID: testDatabaseID, IdentityKey: "binding-1", Access: access}
		material, err := p.IssueCredentials(ctx, req)
		if err != nil {
			t.Fatalf("%s: %v", access, err)
		}
		if err := material.Validate(); err != nil {
			t.Fatalf("%s: material invalid: %v", access, err)
		}
		if material.Username != credentialRoleName(testDatabaseID, "binding-1", access) || material.Database != "gregale" || material.TLSMode != "verify-full" {
			t.Fatalf("%s: material = %+v", access, material)
		}
		if ep := material.Endpoints; len(ep) != 1 || ep[0].Host != testDatabaseID+".db.gregale.test" || ep[0].Port != 5432 {
			t.Fatalf("%s: endpoints = %+v", access, ep)
		}
		batch := sched.lastAdmin()
		if strings.Contains(batch, material.Password) {
			t.Fatalf("%s: the password reached the VM", access)
		}
		v, ok := verifiers.get(testDatabaseID, material.Username)
		if !ok || !passwordMatches(t, v, material.Password) {
			t.Fatalf("%s: published verifier does not match the issued password", access)
		}
		if !strings.Contains(batch, v.String()) {
			t.Fatalf("%s: the VM and the proxy hold different verifiers", access)
		}

		again, err := p.IssueCredentials(ctx, req)
		if err != nil || again.Username != material.Username || again.Password == material.Password {
			t.Fatalf("%s: re-issue must rotate the same role: %v", access, err)
		}
		if v, _ := verifiers.get(testDatabaseID, material.Username); passwordMatches(t, v, material.Password) {
			t.Fatalf("%s: rotated-out password still matches", access)
		}
	}
	if role := credentialRoleName(testDatabaseID, "binding-2", managedpostgres.CredentialReadWrite); role == credentialRoleName(testDatabaseID, "binding-1", managedpostgres.CredentialReadWrite) {
		t.Fatal("a new identity key must get a new role")
	}

	invalid := []managedpostgres.CredentialRequest{
		{IdentityKey: "binding-1", Access: managedpostgres.CredentialReadWrite},
		{ProviderResourceID: testDatabaseID, Access: managedpostgres.CredentialReadWrite},
	}
	for _, req := range invalid {
		if _, err := p.IssueCredentials(ctx, req); !errors.Is(err, managedpostgres.ErrInvalid) {
			t.Errorf("%+v: err = %v", req, err)
		}
	}
	if _, err := p.IssueCredentials(ctx, managedpostgres.CredentialRequest{ProviderResourceID: testDatabaseID, IdentityKey: "k", Access: managedpostgres.CredentialDataAPI}); !errors.Is(err, managedpostgres.ErrUnsupported) {
		t.Fatalf("data API access = %v", err)
	}
}

func TestIssueCredentialsFailuresPublishNothing(t *testing.T) {
	cases := []struct {
		name string
		resp *scheddpb.DatabaseAdminResponse
		err  error
		want error
	}{
		{"privilege assertion", &scheddpb.DatabaseAdminResponse{ErrorCode: "sql_failed", Sqlstate: "P0001"}, nil, managedpostgres.ErrConflict},
		{"sql failure", &scheddpb.DatabaseAdminResponse{ErrorCode: "sql_failed", Sqlstate: "42601"}, nil, managedpostgres.ErrUnavailable},
		{"database gone", nil, status.Error(codes.NotFound, "gone"), managedpostgres.ErrNotFound},
		{"no capacity", nil, status.Error(codes.ResourceExhausted, "full"), managedpostgres.ErrQuotaExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, sched, verifiers := newTestProvider(t)
			sched.vms[testDatabaseID] = &scheddpb.DatabaseRuntime{DatabaseId: testDatabaseID, State: "parked"}
			sched.adminRun = func(string) (*scheddpb.DatabaseAdminResponse, error) { return tc.resp, tc.err }
			_, err := p.IssueCredentials(context.Background(), managedpostgres.CredentialRequest{ProviderResourceID: testDatabaseID, IdentityKey: "k", Access: managedpostgres.CredentialReadWrite})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if len(verifiers.ops) != 0 {
				t.Fatalf("published %v after a failed batch", verifiers.ops)
			}
		})
	}
}

func TestRevokeCredentialsRemovesVerifierFirst(t *testing.T) {
	p, sched, verifiers := newTestProvider(t)
	ctx := context.Background()
	if _, err := p.Provision(ctx, managedpostgres.ProvisionRequest{ResourceID: testDatabaseID, Spec: devSpec()}); err != nil {
		t.Fatal(err)
	}
	req := managedpostgres.CredentialRequest{ProviderResourceID: testDatabaseID, IdentityKey: "binding-1", Access: managedpostgres.CredentialReadOnly}
	material, err := p.IssueCredentials(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	var orderAtAdmin []string
	sched.adminRun = func(string) (*scheddpb.DatabaseAdminResponse, error) {
		orderAtAdmin = append([]string(nil), verifiers.ops...)
		return &scheddpb.DatabaseAdminResponse{}, nil
	}
	if err := p.RevokeCredentials(ctx, req); err != nil {
		t.Fatal(err)
	}
	if _, ok := verifiers.get(testDatabaseID, material.Username); ok {
		t.Fatal("verifier still published")
	}
	if len(orderAtAdmin) == 0 || orderAtAdmin[len(orderAtAdmin)-1] != "delete "+material.Username {
		t.Fatalf("the proxy must refuse the role before the VM is woken to disable it; ops at admin = %v", orderAtAdmin)
	}
	if batch := sched.lastAdmin(); !strings.Contains(batch, "ALTER ROLE "+material.Username+" NOLOGIN") {
		t.Fatalf("revoke batch = %q", batch)
	}

	delete(sched.vms, testDatabaseID)
	if err := p.RevokeCredentials(ctx, req); err != nil {
		t.Fatalf("revoke on a deleted database = %v, want nil", err)
	}
}

func TestDeleteRemovesVMAndVerifiers(t *testing.T) {
	p, sched, verifiers := newTestProvider(t)
	ctx := context.Background()
	if _, err := p.Provision(ctx, managedpostgres.ProvisionRequest{ResourceID: testDatabaseID, Spec: devSpec()}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.IssueCredentials(ctx, managedpostgres.CredentialRequest{ProviderResourceID: testDatabaseID, IdentityKey: "k", Access: managedpostgres.CredentialReadWrite}); err != nil {
		t.Fatal(err)
	}
	for range 2 { // a repeat finds the VM gone and still succeeds
		res, err := p.Delete(ctx, managedpostgres.DeleteRequest{ResourceID: testDatabaseID})
		if err != nil || !res.Done {
			t.Fatalf("delete = %+v, %v", res, err)
		}
	}
	if len(verifiers.byID[testDatabaseID]) != 0 || len(sched.vms) != 0 {
		t.Fatal("delete left a VM or verifiers behind")
	}
	sched.deleteErr = status.Error(codes.Unavailable, "schedd down")
	if _, err := p.Delete(ctx, managedpostgres.DeleteRequest{ProviderResourceID: testDatabaseID}); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("delete with schedd down = %v", err)
	}
}

func TestUnsupportedOperations(t *testing.T) {
	p, _, _ := newTestProvider(t)
	ctx := context.Background()
	if _, err := p.Update(ctx, managedpostgres.UpdateRequest{}); !errors.Is(err, managedpostgres.ErrUnsupported) {
		t.Errorf("update = %v", err)
	}
	if _, err := p.Restore(ctx, managedpostgres.RestoreRequest{}); !errors.Is(err, managedpostgres.ErrUnsupported) {
		t.Errorf("restore = %v", err)
	}
	usage, err := p.Usage(ctx, testDatabaseID, managedpostgres.UsageWindow{})
	if err != nil || len(usage.Readings) != 0 {
		t.Errorf("usage = %+v, %v", usage, err)
	}
}

func TestProviderErrorKeepsScheddContext(t *testing.T) {
	err := providerError(status.Error(codes.FailedPrecondition, "dbsched: database is deleting"))
	if !errors.Is(err, managedpostgres.ErrConflict) || !strings.Contains(err.Error(), "database is deleting") {
		t.Fatalf("err = %v", err)
	}
	if err := providerError(errors.New("transport closed")); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("non-status err = %v", err)
	}
}

func TestSQLBatchesRefuseUnvalidatedInput(t *testing.T) {
	role := credentialRoleName(testDatabaseID, "k", managedpostgres.CredentialReadWrite)
	v, err := pgscram.NewVerifier("pw", pgscram.DefaultIterations)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := initSQL("gregale'; DROP DATABASE postgres; --"); err == nil {
		t.Error("initSQL accepted an injected name")
	}
	if _, err := initSQL("postgres"); err == nil {
		t.Error("initSQL accepted the maintenance database")
	}
	bad := []struct{ database, role, verifier string }{
		{"gregale", "postgres", v.String()},
		{"gregale", role + "'", v.String()},
		{"gregale", role, "md5abc"},
		{"gregale", role, v.String() + "'; DROP ROLE x; --"},
		{"Bad", role, v.String()},
	}
	for _, tc := range bad {
		if _, err := credentialSQL(tc.database, tc.role, managedpostgres.CredentialReadWrite, tc.verifier); err == nil {
			t.Errorf("credentialSQL(%q, %q, %q) accepted", tc.database, tc.role, tc.verifier)
		}
	}
	if _, err := revokeSQL("postgres"); err == nil {
		t.Error("revokeSQL accepted a non-gregale role")
	}
	for _, access := range []managedpostgres.CredentialAccess{managedpostgres.CredentialReadWrite, managedpostgres.CredentialReadOnly, managedpostgres.CredentialMigration} {
		name := credentialRoleName(testDatabaseID, "k", access)
		if !rolePattern.MatchString(name) {
			t.Errorf("%s role %q fails its own pattern", access, name)
		}
	}
}
