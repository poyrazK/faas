// adr: 1027
package firecracker

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
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
	mu            sync.Mutex
	vms           map[string]*scheddpb.DatabaseRuntime
	provision     []*scheddpb.ProvisionDatabaseRequest
	restores      []*scheddpb.RestoreDatabaseRequest
	resizes       []*scheddpb.ResizeDatabaseRequest
	resizePending bool
	// usage answers DatabaseUsage (nil: NotFound); usageReq is the last ask.
	usage    *scheddpb.DatabaseUsageResponse
	usageReq *scheddpb.DatabaseUsageRequest
	admin    []string
	// adminRun, when set, executes admin batches (the pg test runs them
	// through psql); otherwise they succeed.
	adminRun     func(sql string) (*scheddpb.DatabaseAdminResponse, error)
	provisionErr error
	deleteErr    error
	failVM       bool
	// imports are the import requests; importState is what they report.
	imports     []*scheddpb.ImportDatabaseRequest
	importState string
}

func (f *fakeScheduler) ImportDatabase(_ context.Context, in *scheddpb.ImportDatabaseRequest, _ ...grpc.CallOption) (*scheddpb.DatabaseImportProgress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.vms[in.GetDatabaseId()]; !ok {
		return nil, status.Error(codes.NotFound, "no database")
	}
	f.imports = append(f.imports, in)
	f.importState = "running"
	return &scheddpb.DatabaseImportProgress{State: f.importState}, nil
}

func (f *fakeScheduler) DatabaseImportStatus(_ context.Context, in *scheddpb.DatabaseRef, _ ...grpc.CallOption) (*scheddpb.DatabaseImportProgress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.vms[in.GetDatabaseId()]; !ok {
		return nil, status.Error(codes.NotFound, "no database")
	}
	return &scheddpb.DatabaseImportProgress{State: f.importState, SourceMajor: 16}, nil
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
		VcpuCount: in.GetVcpuCount(), MemMib: in.GetMemMib(), VolumeBytes: in.GetVolumeBytes(), AlwaysOn: in.GetAlwaysOn(),
		RestoreWindowSeconds: in.GetRestoreWindowSeconds(), Replicated: in.GetReplicated()}
	f.vms[in.GetDatabaseId()] = rt
	if f.usage == nil {
		f.usage = &scheddpb.DatabaseUsageResponse{} // the ledger opens at placement
	}
	return rt, nil
}

// ResizeDatabase applies the shape at once, unless resizePending holds it
// pending (a resize schedd has recorded but not finished).
func (f *fakeScheduler) ResizeDatabase(_ context.Context, in *scheddpb.ResizeDatabaseRequest, _ ...grpc.CallOption) (*scheddpb.DatabaseRuntime, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resizes = append(f.resizes, in)
	rt, ok := f.vms[in.GetDatabaseId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "dbsched: database not found")
	}
	if in.GetVolumeBytes() < rt.GetVolumeBytes() {
		return nil, status.Error(codes.InvalidArgument, "dbsched: a volume cannot shrink")
	}
	rt.VcpuCount, rt.MemMib, rt.VolumeBytes, rt.ResizePending = in.GetVcpuCount(), in.GetMemMib(), in.GetVolumeBytes(), f.resizePending
	return rt, nil
}

func (f *fakeScheduler) DatabaseUsage(_ context.Context, in *scheddpb.DatabaseUsageRequest, _ ...grpc.CallOption) (*scheddpb.DatabaseUsageResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.usageReq = in
	if f.usage == nil {
		return nil, status.Error(codes.NotFound, "dbsched: database not found")
	}
	return f.usage, nil
}

// RestoreDatabase places the restore provisioning, as schedd does before
// restoring it in the background.
func (f *fakeScheduler) RestoreDatabase(_ context.Context, in *scheddpb.RestoreDatabaseRequest, _ ...grpc.CallOption) (*scheddpb.DatabaseRuntime, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restores = append(f.restores, in)
	if _, ok := f.vms[in.GetSourceDatabaseId()]; !ok {
		return nil, status.Error(codes.NotFound, "dbsched: database not found")
	}
	d := in.GetDatabase()
	if rt, ok := f.vms[d.GetDatabaseId()]; ok {
		return rt, nil
	}
	rt := &scheddpb.DatabaseRuntime{DatabaseId: d.GetDatabaseId(), State: "provisioning", PostgresMajor: d.GetPostgresMajor(),
		VcpuCount: d.GetVcpuCount(), MemMib: d.GetMemMib(), VolumeBytes: d.GetVolumeBytes(), AlwaysOn: d.GetAlwaysOn(),
		RestoreWindowSeconds: d.GetRestoreWindowSeconds(), RestoreSourceId: in.GetSourceDatabaseId(), RestoreTarget: in.GetPointInTime()}
	f.vms[d.GetDatabaseId()] = rt
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

func (f *fakeScheduler) SetDatabaseAlwaysOn(_ context.Context, in *scheddpb.SetDatabaseAlwaysOnRequest, _ ...grpc.CallOption) (*scheddpb.DatabaseRuntime, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rt, ok := f.vms[in.GetDatabaseId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "dbsched: database not found")
	}
	rt.AlwaysOn = in.GetAlwaysOn()
	return rt, nil
}

func (f *fakeScheduler) ParkDatabase(_ context.Context, in *scheddpb.DatabaseRef, _ ...grpc.CallOption) (*scheddpb.DatabaseRuntime, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rt, ok := f.vms[in.GetDatabaseId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "dbsched: database not found")
	}
	rt.State = "parked"
	return rt, nil
}

// setState stands in for schedd moving a database (pgproxyd waking it).
func (f *fakeScheduler) setState(id, state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if rt, ok := f.vms[id]; ok {
		rt.State = state
	}
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
	cfg.CredentialKey = testCredentialKey
	return cfg
}

var testCredentialKey = []byte("test-credential-key-0123456789abcdef")

func newTestProvider(t *testing.T) (*Provider, *fakeScheduler, *fakeVerifiers) {
	t.Helper()
	sched, verifiers := newFakeScheduler(), newFakeVerifiers()
	p, err := New(testConfig(t), sched, verifiers)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{}, sched, verifiers); err == nil {
		t.Fatal("a provider without a credential key was built")
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
		!slices.Equal(cfg.Majors, []int{14, 15, 16, 17, 18}) || cfg.MaxStorageBytes != 100<<30 {
		t.Fatalf("defaults = %+v", cfg)
	}
	cfg, err = ParseConfig(base(settingPort, "6432", settingTLSMode, "require", settingDatabase, "app_db", settingMajors, "16, 17", settingMaxStorage, "2147483648"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 6432 || cfg.TLSMode != "require" || cfg.Database != "app_db" || len(cfg.Majors) != 2 || cfg.MaxStorageBytes != 2<<30 {
		t.Fatalf("overrides = %+v", cfg)
	}
	if cfg.MaxRestoreWindowSeconds != 0 {
		t.Fatalf("point-in-time restore on by default: %+v", cfg)
	}
	if cfg, err = ParseConfig(base(settingMaxRestore, "604800")); err != nil || cfg.MaxRestoreWindowSeconds != 604800 {
		t.Fatalf("restore window = %+v, %v", cfg, err)
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
		{"negative restore window", base(settingMaxRestore, "-1")},
		{"restore window not a number", base(settingMaxRestore, "7d")},
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
	if caps.PointInTimeRestore || caps.RestoreUsageIsolated || len(caps.UsageMeters) != 5 || !caps.ScaleToZero || !caps.ScaleToZeroUpdate || !caps.ClassResize || !caps.StorageResize {
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
		{"restore window", func(s *managedpostgres.Spec) { s.RestoreWindowSeconds = 3600 }, managedpostgres.ErrUnsupported},
		{"other region", func(s *managedpostgres.Spec) { s.Region = "us-east" }, managedpostgres.ErrUnsupported},
		{"unbounded storage", func(s *managedpostgres.Spec) { s.StorageLimitBytes = 0 }, managedpostgres.ErrUnsupported},
		{"storage over max", func(s *managedpostgres.Spec) { s.StorageLimitBytes = 200 << 30 }, managedpostgres.ErrUnsupported},
		{"major not offered", func(s *managedpostgres.Spec) { s.PostgresMajor = 13 }, managedpostgres.ErrUnsupported},
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

func TestAlwaysOnRoundTripsAndToggles(t *testing.T) {
	p, sched, _ := newTestProvider(t)
	ctx := context.Background()
	always := devSpec()
	always.ScaleToZero = false
	obs, err := p.Provision(ctx, managedpostgres.ProvisionRequest{ResourceID: testDatabaseID, Spec: always})
	if err != nil {
		t.Fatal(err)
	}
	if obs.Spec != always || !sched.provision[0].GetAlwaysOn() {
		t.Fatalf("always-on provision: observed %+v, request %+v", obs.Spec, sched.provision[0])
	}
	if obs.DataResourceID != testDatabaseID {
		t.Fatalf("DataResourceID = %q; the service needs it for compute-policy changes", obs.DataResourceID)
	}

	toScale := devSpec()
	obs, err = p.Update(ctx, managedpostgres.UpdateRequest{ResourceID: testDatabaseID, DataResourceID: testDatabaseID, PreviousSpec: always, Spec: toScale})
	if err != nil || obs.Spec != toScale || sched.vms[testDatabaseID].GetAlwaysOn() {
		t.Fatalf("to scale-to-zero: %+v, %v", obs.Spec, err)
	}
	obs, err = p.Update(ctx, managedpostgres.UpdateRequest{ResourceID: testDatabaseID, PreviousSpec: toScale, Spec: always})
	if err != nil || obs.Spec != always {
		t.Fatalf("back to always-on: %+v, %v", obs.Spec, err)
	}

	otherMajor := always
	otherMajor.PostgresMajor = 16
	for name, req := range map[string]managedpostgres.UpdateRequest{
		"major change":        {ResourceID: testDatabaseID, PreviousSpec: always, Spec: otherMajor},
		"other data resource": {ResourceID: testDatabaseID, DataResourceID: "other", PreviousSpec: always, Spec: toScale},
		"missing resource id": {PreviousSpec: always, Spec: toScale},
	} {
		if _, err := p.Update(ctx, req); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := p.Update(ctx, managedpostgres.UpdateRequest{ResourceID: "2b7d3c5e-8f43-4c7a-9d0e-5a1f2b3c4d5e", PreviousSpec: always, Spec: toScale}); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatalf("unknown database = %v", err)
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

		// The contract: re-issuing the same identity recovers the same login,
		// byte for byte, so the VM and pgproxyd keep one verifier.
		again, err := p.IssueCredentials(ctx, req)
		if err != nil || again.Username != material.Username || again.Password != material.Password {
			t.Fatalf("%s: re-issue must return the same login: %v", access, err)
		}
		if v2, _ := verifiers.get(testDatabaseID, material.Username); v2.String() != v.String() {
			t.Fatalf("%s: re-issue changed the verifier", access)
		}
		rotated, err := p.IssueCredentials(ctx, managedpostgres.CredentialRequest{ProviderResourceID: testDatabaseID, IdentityKey: "binding-2", Access: access})
		if err != nil || rotated.Username == material.Username || rotated.Password == material.Password {
			t.Fatalf("%s: a new identity must be a new role and password: %v", access, err)
		}
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
	restore := managedpostgres.RestoreRequest{ResourceID: "1b7d3c5e-8f43-4c7a-9d0e-5a1f2b3c4d5e", SourceResourceID: testDatabaseID, Spec: devSpec(), PointInTime: time.Now()}
	if _, err := p.Restore(ctx, restore); !errors.Is(err, managedpostgres.ErrUnsupported) {
		t.Errorf("restore without point-in-time restore configured = %v", err)
	}
	if _, err := p.Usage(ctx, testDatabaseID, managedpostgres.UsageWindow{}); !errors.Is(err, managedpostgres.ErrInvalid) {
		t.Errorf("usage without a window = %v", err)
	}
}

// TestUsageReadsTheLedger: every advertised meter comes back for the exact
// window asked, and an unknown database is ErrNotFound.
func TestUsageReadsTheLedger(t *testing.T) {
	p, sched, _ := newTestProvider(t)
	ctx := context.Background()
	from := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	window := managedpostgres.UsageWindow{From: from, To: from.Add(time.Hour)}
	sched.usage = &scheddpb.DatabaseUsageResponse{ActiveSeconds: 600, ComputeUnitSeconds: 1200, StorageByteSeconds: 7, HistoryByteSeconds: 8, EgressBytes: 9}
	usage, err := p.Usage(ctx, testDatabaseID, window)
	if err != nil || usage.Window != window {
		t.Fatalf("usage = %+v, %v", usage, err)
	}
	got := map[managedpostgres.Meter]int64{}
	for _, r := range usage.Readings {
		got[r.Meter] = r.Quantity
	}
	for _, meter := range p.Capabilities().UsageMeters {
		if _, ok := got[meter]; !ok {
			t.Errorf("advertised meter %s missing", meter)
		}
	}
	if got[managedpostgres.MeterComputeUnitSeconds] != 1200 || got[managedpostgres.MeterEgressBytes] != 9 {
		t.Fatalf("readings = %v", got)
	}
	if !sched.usageReq.GetFrom().AsTime().Equal(from) || sched.usageReq.GetDatabaseId() != testDatabaseID {
		t.Fatalf("request = %v", sched.usageReq)
	}
	sched.usage = nil
	if _, err := p.Usage(ctx, testDatabaseID, window); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatalf("unknown database = %v", err)
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

func newPITRProvider(t *testing.T) (*Provider, *fakeScheduler) {
	t.Helper()
	cfg := testConfig(t)
	cfg.MaxRestoreWindowSeconds = 7 * 86400
	sched := newFakeScheduler()
	p, err := New(cfg, sched, newFakeVerifiers())
	if err != nil {
		t.Fatal(err)
	}
	return p, sched
}

func TestCapabilitiesWithPointInTimeRestore(t *testing.T) {
	p, _ := newPITRProvider(t)
	caps := p.Capabilities()
	if err := caps.Validate(); err != nil || !caps.PointInTimeRestore || caps.MaxRestoreWindowSeconds != 7*86400 {
		t.Fatalf("capabilities = %+v, %v", caps, err)
	}
	spec := devSpec()
	spec.RestoreWindowSeconds = 86400
	if err := caps.Supports(spec); err != nil {
		t.Fatalf("window within the maximum unsupported: %v", err)
	}
	spec.RestoreWindowSeconds = 8 * 86400
	if err := caps.Supports(spec); !errors.Is(err, managedpostgres.ErrUnsupported) {
		t.Fatalf("window over the maximum = %v", err)
	}
}

// TestRestoreRelaysAndReportsLineage pins what the service checks: the
// lineage comes back from schedd's record, the spec (window included)
// round-trips, and the restore is pending until schedd parks it.
func TestRestoreRelaysAndReportsLineage(t *testing.T) {
	p, sched := newPITRProvider(t)
	ctx := context.Background()
	spec := devSpec()
	spec.RestoreWindowSeconds = 86400
	if _, err := p.Provision(ctx, managedpostgres.ProvisionRequest{ResourceID: testDatabaseID, Spec: spec}); err != nil {
		t.Fatal(err)
	}
	if obs, err := p.Inspect(ctx, testDatabaseID); err != nil || obs.Spec != spec || obs.RestoreLineage != nil {
		t.Fatalf("source = %+v, %v", obs, err)
	}
	const target = "1b7d3c5e-8f43-4c7a-9d0e-5a1f2b3c4d5e"
	point := time.Date(2026, 10, 13, 9, 30, 0, 0, time.UTC)
	req := managedpostgres.RestoreRequest{ResourceID: target, SourceResourceID: testDatabaseID, Spec: spec, PointInTime: point, IdempotencyKey: "restore-x"}
	obs, err := p.Restore(ctx, req)
	if err != nil || obs.ProviderResourceID != target || obs.Status != managedpostgres.ProviderStatusPending || obs.Spec != spec {
		t.Fatalf("restore = %+v, %v", obs, err)
	}
	if obs.RestoreLineage == nil || obs.RestoreLineage.SourceResourceID != testDatabaseID || !obs.RestoreLineage.PointInTime.Equal(point) {
		t.Fatalf("lineage = %+v", obs.RestoreLineage)
	}
	sent := sched.restores[0]
	if sent.GetSourceDatabaseId() != testDatabaseID || !sent.GetPointInTime().AsTime().Equal(point) || sent.GetDatabase().GetMemMib() != 512 || sent.GetDatabase().GetRestoreWindowSeconds() != 86400 {
		t.Fatalf("restore request = %+v", sent)
	}
	sched.setState(target, "parked")
	if obs, err := p.Inspect(ctx, target); err != nil || obs.Status != managedpostgres.ProviderStatusReady || obs.RestoreLineage == nil || obs.Spec != spec {
		t.Fatalf("restored = %+v, %v", obs, err)
	}

	for name, mutate := range map[string]func(*managedpostgres.RestoreRequest){
		"no source":   func(r *managedpostgres.RestoreRequest) { r.SourceResourceID = "" },
		"no point":    func(r *managedpostgres.RestoreRequest) { r.PointInTime = time.Time{} },
		"into itself": func(r *managedpostgres.RestoreRequest) { r.ResourceID = r.SourceResourceID },
	} {
		bad := req
		mutate(&bad)
		if _, err := p.Restore(ctx, bad); !errors.Is(err, managedpostgres.ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
	tooLong := req
	tooLong.Spec.RestoreWindowSeconds = 30 * 86400
	if _, err := p.Restore(ctx, tooLong); !errors.Is(err, managedpostgres.ErrUnsupported) {
		t.Errorf("window over the maximum = %v", err)
	}
	missing := req
	missing.ResourceID, missing.SourceResourceID = "2b7d3c5e-8f43-4c7a-9d0e-5a1f2b3c4d5e", "3b7d3c5e-8f43-4c7a-9d0e-5a1f2b3c4d5e"
	if _, err := p.Restore(ctx, missing); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Errorf("missing source = %v", err)
	}
}

func TestResourceUUIDIsStable(t *testing.T) {
	if got := resourceUUID(testDatabaseID); got != testDatabaseID {
		t.Fatalf("a UUID resource id changed: %s", got)
	}
	if got := resourceUUID(strings.ToUpper(testDatabaseID)); got != testDatabaseID {
		t.Fatalf("an upper-case UUID was not canonicalized: %s", got)
	}
	a, b := resourceUUID("qualification-restore-0123"), resourceUUID("qualification-restore-0123")
	if a != b || a == resourceUUID("qualification-restore-0124") {
		t.Fatalf("derived ids not stable and distinct: %s %s", a, b)
	}
	if _, err := uuid.Parse(a); err != nil {
		t.Fatal(err)
	}
}

// TestUpdateResizesClassAndStorage: a class or storage change becomes one
// schedd resize with the class's fixed shape; storage never shrinks; the
// fixed fields never change; a pending resize observes as pending.
func TestUpdateResizesClassAndStorage(t *testing.T) {
	p, sched, _ := newTestProvider(t)
	ctx := context.Background()
	spec := devSpec()
	if _, err := p.Provision(ctx, managedpostgres.ProvisionRequest{ResourceID: testDatabaseID, Spec: spec}); err != nil {
		t.Fatal(err)
	}
	target := spec
	target.Class, target.StorageLimitBytes = managedpostgres.ClassBurstable, 4<<30
	req := managedpostgres.UpdateRequest{ResourceID: testDatabaseID, PreviousSpec: spec, Spec: target, Generation: 2}
	obs, err := p.Update(ctx, req)
	if err != nil || obs.Status != managedpostgres.ProviderStatusReady || obs.Spec != target {
		t.Fatalf("resize = %+v, %v", obs, err)
	}
	if r := sched.resizes[len(sched.resizes)-1]; r.GetVcpuCount() != 2 || r.GetMemMib() != 1024 || r.GetVolumeBytes() != 4<<30 {
		t.Fatalf("resize request = %v", r)
	}
	// A repeat with the change applied asks schedd nothing new.
	before := len(sched.resizes)
	if _, err := p.Update(ctx, managedpostgres.UpdateRequest{ResourceID: testDatabaseID, PreviousSpec: target, Spec: target, Generation: 3}); err != nil || len(sched.resizes) != before {
		t.Fatalf("no-op update = %v, resizes %d -> %d", err, before, len(sched.resizes))
	}
	sched.resizePending = true
	back := managedpostgres.UpdateRequest{ResourceID: testDatabaseID, PreviousSpec: target, Spec: spec, Generation: 4}
	back.Spec.StorageLimitBytes = target.StorageLimitBytes
	if obs, err := p.Update(ctx, back); err != nil || obs.Status != managedpostgres.ProviderStatusPending {
		t.Fatalf("pending resize = %+v, %v", obs, err)
	}
	for name, mutate := range map[string]func(*managedpostgres.Spec){
		"shrink storage": func(s *managedpostgres.Spec) { s.StorageLimitBytes = 1 << 30 },
		"change major":   func(s *managedpostgres.Spec) { s.PostgresMajor = 16 },
		"change window":  func(s *managedpostgres.Spec) { s.RestoreWindowSeconds = 60 },
	} {
		bad := target
		mutate(&bad)
		if _, err := p.Update(ctx, managedpostgres.UpdateRequest{ResourceID: testDatabaseID, PreviousSpec: target, Spec: bad, Generation: 5}); !errors.Is(err, managedpostgres.ErrUnsupported) {
			t.Errorf("%s = %v, want ErrUnsupported", name, err)
		}
	}
}

// A described backend has the real backend's capabilities but reaches
// nothing: approval tooling can read it without schedd, a database or the
// credential key.
func TestDescribeFactoryReadsCapabilitiesAndReachesNothing(t *testing.T) {
	backend := managedpostgres.BackendConfig{Region: "eu-central", Settings: map[string]string{settingHostSuffix: "db.gregale.test", settingMaxRestore: "86400"}}
	described, err := DescribeFactory()(backend, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	real, _, _ := newTestProvider(t)
	cfg, err := ParseConfig(backend)
	if err != nil {
		t.Fatal(err)
	}
	real.cfg = cfg
	if !reflect.DeepEqual(described.Capabilities(), real.Capabilities()) {
		t.Fatalf("described %+v, real %+v", described.Capabilities(), real.Capabilities())
	}
	ctx := context.Background()
	if _, err := described.Provision(ctx, managedpostgres.ProvisionRequest{ResourceID: testDatabaseID, Spec: devSpec()}); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("provision through a described backend = %v, want ErrUnavailable", err)
	}
	if _, err := described.IssueCredentials(ctx, managedpostgres.CredentialRequest{ProviderResourceID: testDatabaseID, IdentityKey: "k", Access: managedpostgres.CredentialReadWrite}); err == nil {
		t.Fatal("a described backend issued credentials")
	}
}

// The production class runs a synchronous standby on a second node: schedd
// is asked for a replicated database, and the class reads back from it.
// Gaining or losing the standby is not an in-place resize.
func TestProductionClassIsReplicated(t *testing.T) {
	p, sched, _ := newTestProvider(t)
	ctx := context.Background()
	spec := devSpec()
	spec.Class = managedpostgres.ClassProduction
	obs, err := p.Provision(ctx, managedpostgres.ProvisionRequest{ResourceID: testDatabaseID, Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	if len(sched.provision) != 1 || !sched.provision[0].GetReplicated() || sched.provision[0].GetMemMib() != 2048 {
		t.Fatalf("schedd got %+v; want a replicated 2 GiB database", sched.provision)
	}
	if obs.Spec.Class != managedpostgres.ClassProduction {
		t.Fatalf("observed class %q", obs.Spec.Class)
	}
	burstable := spec
	burstable.Class = managedpostgres.ClassBurstable
	if _, err := p.Update(ctx, managedpostgres.UpdateRequest{ResourceID: testDatabaseID, PreviousSpec: spec, Spec: burstable}); !errors.Is(err, managedpostgres.ErrUnsupported) {
		t.Fatalf("production to burstable = %v, want ErrUnsupported", err)
	}
	if !slices.Contains(p.Capabilities().ServiceClasses, managedpostgres.ClassProduction) {
		t.Fatal("capabilities omit the production class")
	}
}

// An import copies into the configured customer database as the schema
// owner, so the usual credentials reach it; the source travels to schedd
// and is never printed.
func TestImportCopiesIntoTheCustomerDatabaseAsItsOwner(t *testing.T) {
	p, sched, _ := newTestProvider(t)
	ctx := context.Background()
	if _, err := p.Provision(ctx, managedpostgres.ProvisionRequest{ResourceID: testDatabaseID, Spec: devSpec()}); err != nil {
		t.Fatal(err)
	}
	src := managedpostgres.ImportSource{Host: "ep-quiet-1.us-east-2.aws.neon.tech", Port: 5432, Database: "gregale", User: "gregale_mig_ab",
		Password: "never-printed", SkipExtensions: []string{"neon"}}
	got, err := p.Import(ctx, managedpostgres.ImportRequest{ResourceID: testDatabaseID, Source: src})
	if err != nil || got.State != managedpostgres.ImportRunning {
		t.Fatalf("import = %+v, %v", got, err)
	}
	req := sched.imports[0]
	if req.GetTargetDatabase() != "gregale" || req.GetOwner() != schemaOwner || req.GetHost() != src.Host || req.GetPassword() != src.Password {
		t.Fatalf("schedd got %v", req)
	}
	if printed := fmt.Sprintf("%v %+v %#v", src, src, src); strings.Contains(printed, "never-printed") {
		t.Fatalf("source printed its password: %s", printed)
	}
	if got, err := p.ImportStatus(ctx, testDatabaseID); err != nil || got.State != managedpostgres.ImportRunning || got.SourceMajor != 16 {
		t.Fatalf("status = %+v, %v", got, err)
	}
	if _, err := p.Import(ctx, managedpostgres.ImportRequest{ResourceID: testDatabaseID, Source: managedpostgres.ImportSource{Host: "h", Port: 0}}); !errors.Is(err, managedpostgres.ErrInvalid) {
		t.Fatalf("no port = %v, want ErrInvalid", err)
	}
	if _, err := p.ImportStatus(ctx, "2b7d3c5e-8f43-4c7a-9d0e-5a1f2b3c4d5e"); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatalf("unknown database = %v, want ErrNotFound", err)
	}
}
