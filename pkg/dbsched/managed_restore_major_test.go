package dbsched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/firecracker"
	"github.com/onebox-faas/faas/pkg/pgscram"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// This bridge uses the real dbsched.Restore validation. Other RPCs are unused.
type historicalRestoreBridge struct {
	firecracker.Scheduler
	scheduler *Scheduler
	t         *testing.T
	reads     int
}

func (b *historicalRestoreBridge) RestoreDatabase(ctx context.Context, in *scheddpb.RestoreDatabaseRequest, _ ...grpc.CallOption) (*scheddpb.DatabaseRuntime, error) {
	d := in.GetDatabase()
	r := RestoreRequest{ProvisionRequest: ProvisionRequest{DatabaseID: d.GetDatabaseId(), Major: int(d.GetPostgresMajor()), VcpuCount: int(d.GetVcpuCount()), MemMiB: int(d.GetMemMib()), VolumeBytes: d.GetVolumeBytes(), AlwaysOn: d.GetAlwaysOn(), RestoreWindowSeconds: d.GetRestoreWindowSeconds(), Replicated: d.GetReplicated()}, Source: in.GetSourceDatabaseId(), Target: in.GetPointInTime().AsTime()}
	source, _ := b.scheduler.Get(ctx, r.Source)
	b.t.Logf("native request major=%d; actual major at requested time=%d", r.Major, source.MajorAt(r.Target))
	vm, err := b.scheduler.Restore(ctx, r)
	if err != nil {
		b.t.Logf("real scheduler rejected: %v", err)
		if errors.Is(err, ErrInvalid) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, err
	}
	return &scheddpb.DatabaseRuntime{DatabaseId: vm.DatabaseID, State: string(vm.State), PostgresMajor: int32(vm.Major), VcpuCount: int32(vm.VcpuCount), MemMib: int32(vm.MemMiB), VolumeBytes: vm.VolumeBytes, AlwaysOn: vm.AlwaysOn, RestoreWindowSeconds: vm.RestoreWindowSeconds, Replicated: vm.Replicated, Bootstrapped: vm.Bootstrapped, RestoreSourceId: vm.RestoreSource, RestoreTarget: timestamppb.New(vm.RestoreTarget)}, nil
}

func (b *historicalRestoreBridge) GetDatabase(ctx context.Context, in *scheddpb.DatabaseRef, _ ...grpc.CallOption) (*scheddpb.DatabaseRuntime, error) {
	b.reads++
	vm, err := b.scheduler.Get(ctx, in.GetDatabaseId())
	if err != nil {
		return nil, err
	}
	rt := &scheddpb.DatabaseRuntime{DatabaseId: vm.DatabaseID, State: string(vm.State), PostgresMajor: int32(vm.Major), VcpuCount: int32(vm.VcpuCount), MemMib: int32(vm.MemMiB), VolumeBytes: vm.VolumeBytes, AlwaysOn: vm.AlwaysOn, RestoreWindowSeconds: vm.RestoreWindowSeconds, Replicated: vm.Replicated, Bootstrapped: vm.Bootstrapped, RestoreSourceId: vm.RestoreSource, CreatedAt: timestamppb.New(vm.CreatedAt), MajorHistoryComplete: true}
	if vm.RestoreSource != "" {
		rt.RestoreTarget = timestamppb.New(vm.RestoreTarget)
	}
	for _, span := range vm.MajorHistory {
		rt.MajorHistory = append(rt.MajorHistory, &scheddpb.DatabaseMajorSpan{PostgresMajor: int32(span.Major), Until: timestamppb.New(span.Until)})
	}
	return rt, nil
}
func (b *historicalRestoreBridge) DatabaseAdmin(ctx context.Context, in *scheddpb.DatabaseAdminRequest, _ ...grpc.CallOption) (*scheddpb.DatabaseAdminResponse, error) {
	resp, err := b.scheduler.Admin(ctx, in.GetDatabaseId(), in.GetSql())
	if err != nil {
		return nil, err
	}
	return &scheddpb.DatabaseAdminResponse{ErrorCode: resp.Error}, nil
}

type historicalRestoreVerifiers struct{}

func (historicalRestoreVerifiers) Put(context.Context, string, string, pgscram.Verifier) error {
	return nil
}
func (historicalRestoreVerifiers) Delete(context.Context, string, string) error { return nil }
func (historicalRestoreVerifiers) DeleteDatabase(context.Context, string) error { return nil }

func TestServiceRestoresBeforeNativeMajorUpgrade(t *testing.T) {
	s, _, vmm := newTestScheduler(t, 4096)
	vmm.archive = true
	ctx := t.Context()
	source, err := s.Provision(ctx, ProvisionRequest{DatabaseID: uuid.NewString(), Major: 16, VcpuCount: 1, MemMiB: 512, VolumeBytes: 1 << 30, RestoreWindowSeconds: 86400})
	if err != nil {
		t.Fatal(err)
	}
	point := time.Now().UTC().Truncate(time.Microsecond)
	time.Sleep(2 * time.Millisecond)
	upgraded, err := s.Upgrade(ctx, source.DatabaseID, 17, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.MajorAt(point) != 16 {
		t.Fatalf("bad historical fixture: %+v", upgraded)
	}
	cfg, err := firecracker.ParseConfig(managedpostgres.BackendConfig{Region: "eu-central", Settings: map[string]string{"host_suffix": "db.test", "max_restore_window_seconds": "86400"}})
	if err != nil {
		t.Fatal(err)
	}
	cfg.CredentialKey = []byte("test-credential-key-0123456789abcdef")
	bridge := &historicalRestoreBridge{scheduler: s, t: t}
	p, err := firecracker.New(cfg, bridge, historicalRestoreVerifiers{})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "eu-central", MaxDatabasesPerAccount: 10, Defaults: map[string]string{"eu-central": "native"}, Backends: []managedpostgres.BackendConfig{{ID: "native", Driver: "firecracker", Region: "eu-central", Namespace: "native"}}}, func(string) string { return "" }, map[string]managedpostgres.Factory{"firecracker": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
		return p, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := reg.Default("eu-central")
	if err != nil {
		t.Fatal(err)
	}
	catalog := managedpostgres.NewMemoryStore()
	now := time.Now().UTC()
	spec := managedpostgres.Spec{Region: "eu-central", PostgresMajor: 17, Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone, ScaleToZero: true, StorageLimitBytes: 1 << 30, RestoreWindowSeconds: 86400}
	d, _, err := catalog.Reserve(ctx, managedpostgres.Database{ID: uuid.NewString(), AccountID: "account-a", Name: "source", Spec: spec, BackendID: b.ID, BackendFingerprint: b.Fingerprint, State: managedpostgres.StateProvisioning, DesiredGeneration: 1, CreatedAt: source.CreatedAt, UpdatedAt: now}, 10)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := catalog.Claim(ctx, d.AccountID, d.ID, "claim", managedpostgres.StateProvisioning, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err = catalog.RecordProviderResource(ctx, d.ID, claimed.LeaseToken, source.DatabaseID, now); err != nil {
		t.Fatal(err)
	}
	claimed.ProviderResourceID = source.DatabaseID
	d, err = catalog.FinishProvisionWithDataResource(ctx, claimed, managedpostgres.ObservedDatabase{ProviderResourceID: source.DatabaseID, DataResourceID: source.DatabaseID, Spec: spec, Status: managedpostgres.ProviderStatusReady}, now)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Now().UTC()
	svc, err := managedpostgres.NewService(reg, catalog, managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return true }, Now: func() time.Time { return clock }, PollInterval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := svc.Restore(ctx, managedpostgres.RestoreDatabaseRequest{AccountID: d.AccountID, SourceDatabaseID: d.ID, Name: "before-upgrade", PointInTime: point})
	s.restoreWG.Wait()
	if err != nil || restored.Spec.PostgresMajor != 16 {
		t.Fatalf("historical restore: %+v %v", restored, err)
	}
	clock = clock.Add(2 * time.Second)
	restored, err = svc.Reconcile(ctx, d.AccountID, restored.ID)
	if err != nil || restored.State != managedpostgres.StateReady || restored.Spec.PostgresMajor != 16 {
		t.Fatalf("restore completion: %+v %v", restored, err)
	}
	reads := bridge.reads
	again, err := svc.Restore(ctx, managedpostgres.RestoreDatabaseRequest{AccountID: d.AccountID, SourceDatabaseID: d.ID, Name: "before-upgrade", PointInTime: point})
	if err != nil || again.ID != restored.ID || bridge.reads != reads {
		t.Fatalf("replay resolved new history: %+v %v reads=%d/%d", again, err, bridge.reads, reads)
	}
	sourceNow, _ := s.Get(ctx, source.DatabaseID)
	if sourceNow.Major != 17 {
		t.Fatal("restoring changed the source major")
	}
}
