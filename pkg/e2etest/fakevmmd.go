// fakevmmd.go — the shared KVM-free vmmd stand-in for the e2e harness.
//
// It speaks the real ForwardHTTPStream protocol over a unix socket, so a test
// exercises the production gateway route/cache -> gRPC bridge and the schedd
// wake path without /dev/kvm. It is NOT a Firecracker simulator: whether a
// snapshot can actually be loaded is the metal gate's question. This models
// the RPC contract around that, which is where the control-plane bugs live.
//
// It lived inside cmd/e2e/normal_path_e2e_test.go until now, which is why only
// one e2e family could use it. Everything below is a move plus the renames the
// package boundary forces; behaviour is unchanged.
//
// Coverage note: this fake implements 11 of vmmd's 36 RPCs — Ping, Heartbeat,
// CreateColdBoot, CreateFromSnapshot, PauseAndSnapshot, Destroy, StopInstance,
// Stats, FrameworkReady, UpdateEgressAllowlist, ForwardHTTPStream. The rest fall through to
// UnimplementedVmmdServer, so any daemon path that needs one is silently
// unreachable from CI. Grow this deliberately rather than assuming a green e2e
// run covered a boundary it never called.
//
// Fidelity matters more than completeness here. When Destroy and StopInstance
// were Unimplemented the reaper could never tear an instance down, so seeded
// instances lived forever and no e2e request ever performed a real wake — the
// suite looked like it covered the lifecycle while exercising none of it.

package e2etest

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type CancellationProbe struct {
	blockRequestBody  bool
	blockResponseBody bool
	initSeen          chan struct{}
	firstBodySeen     chan struct{}
	headersSent       chan struct{}
	firstResponseBody chan struct{}
	canceled          chan struct{}
	release           chan struct{}
	initOnce          sync.Once
	firstBodyOnce     sync.Once
	headersOnce       sync.Once
	firstResponseOnce sync.Once
	canceledOnce      sync.Once
	releaseOnce       sync.Once
}

func newCancellationProbe(blockRequestBody, blockResponseBody bool) *CancellationProbe {
	return &CancellationProbe{
		blockRequestBody:  blockRequestBody,
		blockResponseBody: blockResponseBody,
		initSeen:          make(chan struct{}),
		firstBodySeen:     make(chan struct{}),
		headersSent:       make(chan struct{}),
		firstResponseBody: make(chan struct{}),
		canceled:          make(chan struct{}),
		release:           make(chan struct{}),
	}
}

func (p *CancellationProbe) markInit() {
	p.initOnce.Do(func() { close(p.initSeen) })
}

func (p *CancellationProbe) markFirstBody() {
	p.firstBodyOnce.Do(func() { close(p.firstBodySeen) })
}

func (p *CancellationProbe) markHeadersSent() {
	p.headersOnce.Do(func() { close(p.headersSent) })
}

func (p *CancellationProbe) markFirstResponseBody() {
	p.firstResponseOnce.Do(func() { close(p.firstResponseBody) })
}

func (p *CancellationProbe) markCanceled() {
	p.canceledOnce.Do(func() { close(p.canceled) })
}

func (p *CancellationProbe) Release() {
	p.releaseOnce.Do(func() { close(p.release) })
}

type FakeVMMD struct {
	vmmdpb.UnimplementedVmmdServer
	mu               sync.Mutex
	versions         map[string]string
	responses        map[string]FakeResponse
	responsesByPath  map[string]map[string]FakeResponse
	responseSequence map[string][]FakeResponse
	failNext         map[string]error
	failures         map[string]error
	probes           map[string]*CancellationProbe
	gates            map[string]*RequestGate
	requests         []RequestCapture
	last             *vmmdpb.ForwardHTTPRequestInit
	lastBody         []byte
	lastBodyChunks   int
	forwardCount     int
	defaultVersion   string

	// Lifecycle-RPC bookkeeping. Before these existed the fake implemented
	// four of vmmd's thirty-six RPCs (Ping, Heartbeat, CreateColdBoot,
	// ForwardHTTPStream) and inherited Unimplemented for the rest, so PR CI
	// could not observe restore-vs-cold-boot selection, park, or teardown at
	// all. Recording the calls — rather than only their side effects — is
	// what lets a test assert which wake edge schedd actually took, which is
	// the distinction ADR-005 turns on.
	restoreCalls  []*vmmdpb.CreateFromSnapshotRequest
	coldBootCalls []*vmmdpb.CreateColdBootRequest
	snapshotCalls []*vmmdpb.PauseAndSnapshotRequest
	destroyCalls  []string
	stopCalls     []string
	failRestore   error
	failSnapshot  error
	degradeWake   bool

	// liveInstances / instanceStats back the Stats RPC: schedd's view of what
	// is resident on the node. Kept in boot order so a test reading Stats sees
	// a stable sequence.
	liveInstances  []string
	instanceStats  map[string]*vmmdpb.InstanceStats
	frameworkReady []*vmmdpb.FrameworkReadyRequest
	egressUpdates  []*vmmdpb.UpdateEgressAllowlistRequest

	// unreachable makes the liveness RPCs fail, which is how a node that has
	// died looks to schedd. Backdating last_heartbeat_at is not enough on its
	// own: schedd keeps probing, and a fake that keeps answering refreshes the
	// row within a tick, so the node never actually looks stale.
	unreachable bool

	// strictInstances makes ForwardHTTPStream refuse an instance the fake has
	// not seen boot. Off by default: most tests seed RUNNING rows straight
	// into SQL and never boot through this fake at all, and refusing those
	// would break them for no gain.
	//
	// Tests about instance lifecycle need it on. Without it the fake answers
	// for ANY instance id, so a gateway routing to a destroyed VM still gets a
	// 200 — and an assertion that "the app recovered" passes on a stale route
	// to a dead instance. A real vmmd has no such instance and fails.
	strictInstances bool
}

type FakeResponse struct {
	Status   int
	Headers  []*vmmdpb.Header
	Trailers []*vmmdpb.Header
	Body     []byte
	Chunks   [][]byte
}

type RequestCapture struct {
	Init *vmmdpb.ForwardHTTPRequestInit
	Body []byte
}

// RequestGate deliberately blocks the fake VMMD after it has
// received a complete request. It lets the E2E tests prove that several
// customer requests are genuinely in flight together, or that a saturated
// instance keeps the next request outside the bridge until the first one
// releases its gateway slot.
type RequestGate struct {
	want        int
	arrived     chan struct{}
	release     chan struct{}
	mu          sync.Mutex
	count       int
	arrivedOnce sync.Once
	releaseOnce sync.Once
}

func newRequestGate(want int) *RequestGate {
	return &RequestGate{
		want:    want,
		arrived: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (g *RequestGate) Block(ctx context.Context) error {
	g.mu.Lock()
	g.count++
	if g.count >= g.want {
		g.arrivedOnce.Do(func() { close(g.arrived) })
	}
	g.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-g.release:
		return nil
	}
}

func (g *RequestGate) WaitArrived(timeout time.Duration) bool {
	select {
	case <-g.arrived:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (g *RequestGate) Release() {
	g.releaseOnce.Do(func() { close(g.release) })
}

const (
	// FakeSnapshotRAMMB is the RAM size the fake reports for a captured
	// snapshot. Engine.snapshotMatchesRAM rejects a snapshot whose mem_bytes
	// disagrees with the admitted instance size, so a test that seeds both has
	// to use this for each or the wake cold-boots for a reason it was not
	// testing.
	FakeSnapshotRAMMB = 256

	// FakeFCVersion is what tests pin schedd's Firecracker version to via
	// FAAS_SCHEDD_FC_VERSION. CI has no firecracker binary, so detection would
	// leave the version "" and Engine.snapshotCompatible would reject every
	// snapshot — making the restore path unreachable. See cmd/schedd/fcversion.go.
	FakeFCVersion = "1.7.0-e2e"
)

func StartFakeVMMD(t *testing.T, socketPath string) *FakeVMMD {
	t.Helper()
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen fake vmmd: %v", err)
	}
	server := grpc.NewServer()
	vmmd := &FakeVMMD{
		versions:         make(map[string]string),
		responses:        make(map[string]FakeResponse),
		responsesByPath:  make(map[string]map[string]FakeResponse),
		responseSequence: make(map[string][]FakeResponse),
		failNext:         make(map[string]error),
		failures:         make(map[string]error),
		probes:           make(map[string]*CancellationProbe),
		gates:            make(map[string]*RequestGate),
		instanceStats:    make(map[string]*vmmdpb.InstanceStats),
	}
	vmmdpb.RegisterVmmdServer(server, vmmd)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Logf("fake vmmd stopped: %v", err)
		}
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	return vmmd
}

func (s *FakeVMMD) SetVersion(instanceID, version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versions[instanceID] = version
}

func (s *FakeVMMD) SetDefaultVersion(version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaultVersion = version
}

func (s *FakeVMMD) FailNext(instanceID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNext[instanceID] = err
}

// FailAll keeps returning err for this instance until the gateway evicts it.
// It models a dead VMMD/netns rather than a single transient bridge failure.
func (s *FakeVMMD) FailAll(instanceID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[instanceID] = err
}

func (s *FakeVMMD) SetResponse(instanceID string, response FakeResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses[instanceID] = cloneResponse(response)
	delete(s.responseSequence, instanceID)
}

func (s *FakeVMMD) SetResponseForPath(instanceID, requestURI string, response FakeResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	byPath := s.responsesByPath[instanceID]
	if byPath == nil {
		byPath = make(map[string]FakeResponse)
		s.responsesByPath[instanceID] = byPath
	}
	byPath[requestURI] = cloneResponse(response)
}

func (s *FakeVMMD) SetResponseSequence(instanceID string, responses []FakeResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sequence := make([]FakeResponse, len(responses))
	for i, response := range responses {
		sequence[i] = cloneResponse(response)
	}
	s.responseSequence[instanceID] = sequence
}

func (s *FakeVMMD) LastRequest() *vmmdpb.ForwardHTTPRequestInit {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		return nil
	}
	return proto.Clone(s.last).(*vmmdpb.ForwardHTTPRequestInit)
}

func (s *FakeVMMD) LastBody() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.lastBody...)
}

func (s *FakeVMMD) LastBodyChunkCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastBodyChunks
}

func (s *FakeVMMD) ForwardCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.forwardCount
}

func (s *FakeVMMD) Requests() []RequestCapture {
	s.mu.Lock()
	defer s.mu.Unlock()
	captures := make([]RequestCapture, len(s.requests))
	for i, capture := range s.requests {
		captures[i] = RequestCapture{
			Init: proto.Clone(capture.Init).(*vmmdpb.ForwardHTTPRequestInit),
			Body: append([]byte(nil), capture.Body...),
		}
	}
	return captures
}

func (s *FakeVMMD) InstallCancellationProbe(instanceID string, blockRequestBody, blockResponseBody bool) *CancellationProbe {
	s.mu.Lock()
	defer s.mu.Unlock()
	probe := newCancellationProbe(blockRequestBody, blockResponseBody)
	s.probes[instanceID] = probe
	return probe
}

func (s *FakeVMMD) ReleaseProbe(probe *CancellationProbe) {
	if probe != nil {
		probe.Release()
	}
}

func (s *FakeVMMD) InstallRequestGate(instanceID string, want int) *RequestGate {
	s.mu.Lock()
	defer s.mu.Unlock()
	gate := newRequestGate(want)
	s.gates[instanceID] = gate
	return gate
}

func (s *FakeVMMD) Heartbeat(context.Context, *vmmdpb.HeartbeatRequest) (*vmmdpb.HeartbeatResponse, error) {
	if s.isUnreachable() {
		return nil, status.Error(codes.Unavailable, "fake vmmd: node is unreachable")
	}
	return &vmmdpb.HeartbeatResponse{}, nil
}

func (s *FakeVMMD) Ping(context.Context, *vmmdpb.PingRequest) (*vmmdpb.PingResponse, error) {
	if s.isUnreachable() {
		return nil, status.Error(codes.Unavailable, "fake vmmd: node is unreachable")
	}
	return &vmmdpb.PingResponse{}, nil
}

// SetUnreachable makes the liveness RPCs (Ping, Heartbeat) fail, simulating a
// node that has stopped answering — a crashed vmmd, a severed link, a box that
// went away. Set it back to false to bring the node back.
//
// This is the difference between a node that LOOKS stale for one moment and a
// node that IS down: schedd keeps probing, so a fake that still answers
// refreshes last_heartbeat_at on the next tick no matter how far back a test
// dated it.
func (s *FakeVMMD) SetUnreachable(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unreachable = v
}

// SetStrictInstances makes the bridge refuse instances the fake never booted,
// so a stale route to a destroyed VM fails the way it would against a real
// vmmd instead of being quietly served.
func (s *FakeVMMD) SetStrictInstances(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.strictInstances = v
}

// ForgetInstances drops every instance the fake considers resident. This is
// what a host death means: the VMs went with it. Pair it with
// SetUnreachable(true) to model a node that died rather than one that is
// merely slow.
func (s *FakeVMMD) ForgetInstances() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.liveInstances = nil
	s.instanceStats = make(map[string]*vmmdpb.InstanceStats)
}

func (s *FakeVMMD) rejectsInstance(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.strictInstances {
		return false
	}
	for _, live := range s.liveInstances {
		if live == id {
			return false
		}
	}
	return true
}

func (s *FakeVMMD) isUnreachable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unreachable
}

func (s *FakeVMMD) CreateColdBoot(_ context.Context, request *vmmdpb.CreateColdBootRequest) (*vmmdpb.WakeResponse, error) {
	s.mu.Lock()
	s.coldBootCalls = append(s.coldBootCalls, proto.Clone(request).(*vmmdpb.CreateColdBootRequest))
	s.trackLiveLocked(request.GetInstance())
	s.mu.Unlock()
	return &vmmdpb.WakeResponse{
		Instance: request.GetInstance(),
		LeaseUid: 20000,
		HostIp:   "127.0.0.1",
		Netns:    "fake-" + request.GetInstance(),
		Method:   vmmdpb.WakeMethod_WAKE_COLD_BOOT,
	}, nil
}

// CreateFromSnapshot is the restore edge — what every production wake of a
// parked app actually does. It was Unimplemented here until now, so the whole
// e2e suite silently exercised only the cold-boot path.
//
// Two distinct failure shapes matter, and schedd treats them very differently
// (engine.go, the CreateFromSnapshot call site):
//
//   - DegradeRestoreToColdBoot: vmmd could not load the snapshot and booted
//     the rootfs instead, reporting Method=WAKE_COLD_BOOT against
//     RequestedMethod=WAKE_RESTORE. This is where ADR-005's fallback actually
//     lives — inside vmmd, not schedd. The wake SUCCEEDS and schedd retires
//     the snapshot on the method mismatch.
//   - FailRestore: the RPC itself errors. schedd does NOT fall back here; it
//     releases the ledger reservation and transitions the instance to FAILED.
//     The customer request fails.
//
// Conflating the two is easy to do from the spec prose alone, so both are
// modelled explicitly.
func (s *FakeVMMD) CreateFromSnapshot(_ context.Context, request *vmmdpb.CreateFromSnapshotRequest) (*vmmdpb.WakeResponse, error) {
	s.mu.Lock()
	s.restoreCalls = append(s.restoreCalls, proto.Clone(request).(*vmmdpb.CreateFromSnapshotRequest))
	failure := s.failRestore
	degrade := s.degradeWake
	if failure == nil {
		s.trackLiveLocked(request.GetInstance())
	}
	s.mu.Unlock()
	if failure != nil {
		return nil, failure
	}
	method := vmmdpb.WakeMethod_WAKE_RESTORE
	if degrade {
		method = vmmdpb.WakeMethod_WAKE_COLD_BOOT
	}
	return &vmmdpb.WakeResponse{
		Instance:        request.GetInstance(),
		LeaseUid:        20001,
		HostIp:          "127.0.0.1",
		Netns:           "fake-" + request.GetInstance(),
		Method:          method,
		RequestedMethod: vmmdpb.WakeMethod_WAKE_RESTORE,
	}, nil
}

// PauseAndSnapshot is the park edge. A failure here must leave the app
// cold-bootable rather than parked (spec §6.0: snapshot failure ends at
// `stopped`, never at a state that implies a snapshot exists).
func (s *FakeVMMD) PauseAndSnapshot(_ context.Context, request *vmmdpb.PauseAndSnapshotRequest) (*vmmdpb.SnapshotResponse, error) {
	s.mu.Lock()
	s.snapshotCalls = append(s.snapshotCalls, proto.Clone(request).(*vmmdpb.PauseAndSnapshotRequest))
	failure := s.failSnapshot
	s.mu.Unlock()
	if failure != nil {
		return nil, failure
	}
	return &vmmdpb.SnapshotResponse{
		MemBytes:     int64(FakeSnapshotRAMMB) << 20,
		VmstateBytes: 4096,
		StoredBytes:  int64(FakeSnapshotRAMMB)<<20 + 4096,
	}, nil
}

func (s *FakeVMMD) Destroy(_ context.Context, request *vmmdpb.DestroyRequest) (*vmmdpb.DestroyResponse, error) {
	s.mu.Lock()
	s.destroyCalls = append(s.destroyCalls, request.GetInstance())
	s.forgetLiveLocked(request.GetInstance())
	s.mu.Unlock()
	return &vmmdpb.DestroyResponse{Instance: request.GetInstance()}, nil
}

func (s *FakeVMMD) StopInstance(_ context.Context, request *vmmdpb.StopInstanceRequest) (*vmmdpb.StopInstanceResponse, error) {
	s.mu.Lock()
	s.stopCalls = append(s.stopCalls, request.GetInstance())
	s.forgetLiveLocked(request.GetInstance())
	s.mu.Unlock()
	return &vmmdpb.StopInstanceResponse{Instance: request.GetInstance()}, nil
}

// Stats is the per-node telemetry schedd polls: residency for the RAM ledger,
// in-flight counts for the reaper's idle decision, and request counters that
// feed metering. It returns one InstanceStats row per instance the fake has
// seen boot and not seen destroyed, so a test can assert that schedd's view of
// the node matches what it actually asked for.
//
// SetInstanceStats overrides a row when a test needs specific numbers (an idle
// instance the reaper should park, a busy one it must not).
func (s *FakeVMMD) Stats(context.Context, *vmmdpb.StatsRequest) (*vmmdpb.StatsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	resp := &vmmdpb.StatsResponse{}
	var resident int64
	for _, id := range s.liveInstances {
		stat, ok := s.instanceStats[id]
		if !ok {
			stat = &vmmdpb.InstanceStats{
				Instance:      id,
				LeaseUid:      20000,
				HostIp:        "127.0.0.1",
				ResidentBytes: wrapperspb.Int64(int64(FakeSnapshotRAMMB) << 20),
			}
		}
		resident += stat.GetResidentBytes().GetValue()
		resp.Instances = append(resp.Instances, proto.Clone(stat).(*vmmdpb.InstanceStats))
	}
	resp.LiveCount = int32(len(resp.Instances))
	resp.LeasedCount = resp.LiveCount
	resp.TotalResidentBytes = wrapperspb.Int64(resident)
	return resp, nil
}

// SetInstanceStats pins the InstanceStats row the fake reports for one
// instance. The instance is treated as live until Destroy or StopInstance.
func (s *FakeVMMD) SetInstanceStats(instanceID string, stat *vmmdpb.InstanceStats) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if stat.GetInstance() == "" {
		stat.Instance = instanceID
	}
	s.instanceStats[instanceID] = stat
	s.trackLiveLocked(instanceID)
}

// FrameworkReady is the guest's post-boot readiness signal — the point schedd
// treats the framework as warm and therefore worth a warm-tier snapshot
// (ADR-074). Recording the warmup lets a test assert the handshake happened
// and with what duration, rather than only that a boot returned.
func (s *FakeVMMD) FrameworkReady(_ context.Context, request *vmmdpb.FrameworkReadyRequest) (*vmmdpb.FrameworkReadyResponse, error) {
	s.mu.Lock()
	s.frameworkReady = append(s.frameworkReady, proto.Clone(request).(*vmmdpb.FrameworkReadyRequest))
	s.mu.Unlock()
	return &vmmdpb.FrameworkReadyResponse{}, nil
}

// UpdateEgressAllowlist receives the per-app outbound allowlist schedd fans
// out when apps.egress_allowlist changes (ADR-031/033).
//
// Recording it is the whole point. Enforcement is nftables inside the netns
// and needs metal, but whether the intended policy ever REACHES the node is
// pure control plane — and a policy that is committed in Postgres and never
// delivered leaves a tenant running on its old rules with nothing to show for
// it. That is the failure this makes visible.
func (s *FakeVMMD) UpdateEgressAllowlist(_ context.Context, req *vmmdpb.UpdateEgressAllowlistRequest) (*vmmdpb.UpdateEgressAllowlistAck, error) {
	s.mu.Lock()
	s.egressUpdates = append(s.egressUpdates, proto.Clone(req).(*vmmdpb.UpdateEgressAllowlistRequest))
	s.mu.Unlock()
	return &vmmdpb.UpdateEgressAllowlistAck{}, nil
}

// EgressUpdates returns the allowlist pushes the fake received, in order.
func (s *FakeVMMD) EgressUpdates() []*vmmdpb.UpdateEgressAllowlistRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*vmmdpb.UpdateEgressAllowlistRequest(nil), s.egressUpdates...)
}

// FrameworkReadyCalls returns the readiness signals the fake received.
func (s *FakeVMMD) FrameworkReadyCalls() []*vmmdpb.FrameworkReadyRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*vmmdpb.FrameworkReadyRequest(nil), s.frameworkReady...)
}

// trackLiveLocked records an instance as resident. Callers hold s.mu.
func (s *FakeVMMD) trackLiveLocked(instanceID string) {
	if instanceID == "" {
		return
	}
	for _, id := range s.liveInstances {
		if id == instanceID {
			return
		}
	}
	s.liveInstances = append(s.liveInstances, instanceID)
}

// forgetLiveLocked drops an instance from the resident set. Callers hold s.mu.
func (s *FakeVMMD) forgetLiveLocked(instanceID string) {
	kept := s.liveInstances[:0]
	for _, id := range s.liveInstances {
		if id != instanceID {
			kept = append(kept, id)
		}
	}
	s.liveInstances = kept
	delete(s.instanceStats, instanceID)
}

// FailRestore makes the next and every subsequent CreateFromSnapshot return a
// gRPC error, which schedd treats as a terminal wake failure rather than as a
// reason to cold boot.
func (s *FakeVMMD) FailRestore(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failRestore = err
}

// DegradeRestoreToColdBoot reproduces vmmd's own ADR-005 fallback: the restore
// RPC succeeds, but vmmd reports it booted the rootfs instead of loading the
// snapshot. schedd must accept the wake and retire the snapshot.
func (s *FakeVMMD) DegradeRestoreToColdBoot() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.degradeWake = true
}

// FailSnapshot makes PauseAndSnapshot fail, exercising the park path that must
// degrade to `stopped` rather than claiming a snapshot exists.
func (s *FakeVMMD) FailSnapshot(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failSnapshot = err
}

func (s *FakeVMMD) RestoreCalls() []*vmmdpb.CreateFromSnapshotRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*vmmdpb.CreateFromSnapshotRequest(nil), s.restoreCalls...)
}

func (s *FakeVMMD) ColdBootCalls() []*vmmdpb.CreateColdBootRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*vmmdpb.CreateColdBootRequest(nil), s.coldBootCalls...)
}

func (s *FakeVMMD) SnapshotCalls() []*vmmdpb.PauseAndSnapshotRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*vmmdpb.PauseAndSnapshotRequest(nil), s.snapshotCalls...)
}

func (s *FakeVMMD) DestroyCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.destroyCalls...)
}

func (s *FakeVMMD) StopCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.stopCalls...)
}

func (s *FakeVMMD) ForwardHTTPStream(stream vmmdpb.Vmmd_ForwardHTTPStreamServer) error {
	request, err := stream.Recv()
	if err != nil {
		return err
	}
	init := request.GetInit()
	if init == nil {
		return errors.New("fake vmmd: first frame was not init")
	}
	if s.rejectsInstance(init.Instance) {
		return status.Errorf(codes.Unavailable,
			"fake vmmd: no such instance %q on this node", init.Instance)
	}
	s.mu.Lock()
	probe := s.probes[init.Instance]
	s.mu.Unlock()
	if probe != nil {
		defer func() {
			if stream.Context().Err() != nil {
				probe.markCanceled()
			}
		}()
		probe.markInit()
	}
	var body []byte
	bodyChunkCount := 0
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if chunk := frame.GetBodyChunk(); chunk != nil {
			bodyChunkCount++
			body = append(body, chunk...)
			if probe != nil {
				probe.markFirstBody()
				if probe.blockRequestBody && bodyChunkCount == 1 {
					select {
					case <-stream.Context().Done():
						return stream.Context().Err()
					case <-probe.release:
					}
				}
			}
		}
	}

	s.mu.Lock()
	s.last = init
	s.lastBody = append([]byte(nil), body...)
	s.lastBodyChunks = bodyChunkCount
	s.forwardCount++
	s.requests = append(s.requests, RequestCapture{
		Init: proto.Clone(init).(*vmmdpb.ForwardHTTPRequestInit),
		Body: append([]byte(nil), body...),
	})
	version := s.versions[init.Instance]
	if version == "" {
		version = s.defaultVersion
	}
	response := s.responses[init.Instance]
	if byPath := s.responsesByPath[init.Instance]; byPath != nil {
		if pathResponse, ok := byPath[init.RequestUri]; ok {
			response = pathResponse
		}
	}
	if sequence := s.responseSequence[init.Instance]; len(sequence) > 0 {
		response = sequence[0]
		s.responseSequence[init.Instance] = sequence[1:]
	}
	failure := s.failures[init.Instance]
	if nextFailure := s.failNext[init.Instance]; nextFailure != nil {
		failure = nextFailure
	}
	gate := s.gates[init.Instance]
	delete(s.failNext, init.Instance)
	s.mu.Unlock()
	if gate != nil {
		if err := gate.Block(stream.Context()); err != nil {
			return err
		}
	}
	if failure != nil {
		return failure
	}
	if version == "" {
		return errors.New("fake vmmd: unknown instance " + init.Instance)
	}
	if response.Status == 0 {
		response.Status = http.StatusOK
	}
	if len(response.Headers) == 0 {
		response.Headers = []*vmmdpb.Header{{Name: "Content-Type", Value: "text/plain"}}
	}
	chunks := response.Chunks
	if len(chunks) == 0 {
		if response.Body == nil {
			response.Body = []byte("normal-path:" + version + "\n")
		}
		if len(response.Body) > 0 {
			chunks = [][]byte{response.Body}
		}
	}
	if err := stream.Send(&vmmdpb.ForwardHTTPStreamResponse{
		Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{
			Status:   int32(response.Status),
			Headers:  response.Headers,
			Trailers: response.Trailers,
		}},
	}); err != nil {
		return err
	}
	if probe != nil {
		probe.markHeadersSent()
	}
	for _, chunk := range chunks {
		if len(chunk) == 0 {
			continue
		}
		if err := stream.Send(&vmmdpb.ForwardHTTPStreamResponse{
			Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: chunk},
		}); err != nil {
			return err
		}
		if probe != nil && probe.blockResponseBody {
			probe.markFirstResponseBody()
			select {
			case <-stream.Context().Done():
				return stream.Context().Err()
			case <-probe.release:
			}
		}
	}
	if len(response.Trailers) > 0 {
		if err := stream.Send(&vmmdpb.ForwardHTTPStreamResponse{
			Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{
				Trailers: response.Trailers,
			}},
		}); err != nil {
			return err
		}
	}
	return nil
}

func cloneChunks(chunks [][]byte) [][]byte {
	if len(chunks) == 0 {
		return nil
	}
	out := make([][]byte, len(chunks))
	for i, chunk := range chunks {
		out[i] = append([]byte(nil), chunk...)
	}
	return out
}

func cloneResponse(response FakeResponse) FakeResponse {
	response.Headers = append([]*vmmdpb.Header(nil), response.Headers...)
	response.Trailers = append([]*vmmdpb.Header(nil), response.Trailers...)
	response.Body = append([]byte(nil), response.Body...)
	response.Chunks = cloneChunks(response.Chunks)
	return response
}

// The probe's signal channels are read from other packages, so they are
// exposed as receive-only accessors rather than as exported fields — a test
// must be able to wait on a signal, never to fire one.

// InitSeen closes once the bridge has received the request's init frame.
func (p *CancellationProbe) InitSeen() <-chan struct{} { return p.initSeen }

// FirstBodySeen closes once the first request body chunk has arrived.
func (p *CancellationProbe) FirstBodySeen() <-chan struct{} { return p.firstBodySeen }

// HeadersSent closes once the fake has sent its response init frame.
func (p *CancellationProbe) HeadersSent() <-chan struct{} { return p.headersSent }

// FirstResponseBody closes once the first response body chunk has been sent.
func (p *CancellationProbe) FirstResponseBody() <-chan struct{} { return p.firstResponseBody }

// Canceled closes once the bridge stream's context was canceled, which is how
// a test observes that the gateway propagated a client disconnect.
func (p *CancellationProbe) Canceled() <-chan struct{} { return p.canceled }
