package sched_test

// adr: 631
import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/pki"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type retirementPeerResolver map[string]string

func (r retirementPeerResolver) NodeIDByCN(cn string) (string, error) {
	if n := r[cn]; n != "" {
		return n, nil
	}
	return "", errors.New("unknown native peer")
}

// This is portable transport simulation, not native process/resource proof.
type retirementWireVMM struct {
	*fakeVMM
	store        *state.MemStore
	calls        atomic.Int64
	firstFence   string
	mutate       func(*runtimequota.CapacityStop)
	wantInstance string
}

func (v *retirementWireVMM) RetireRegionalRuntime(ctx context.Context, instance string, p runtimequota.CapacityPermit, fence string) (runtimequota.CapacityStop, error) {
	v.calls.Add(1)
	r, err := v.store.RegionalRuntimeCapacity(ctx, p.Grant.Binding.RegionalAppID, p.ID)
	if err != nil || r.Permit != p || r.FenceID != fence || r.IssuedAt == nil || r.ReleasedAt != nil || (v.wantInstance != "" && instance != v.wantInstance) {
		return runtimequota.CapacityStop{}, fmt.Errorf("native dispatch preceded exact durable fence")
	}
	stop := runtimequota.CapacityStop{Permit: p, FenceID: fence, AdmissionFrozen: true}
	if v.firstFence != "" {
		stop.FenceID = v.firstFence
	}
	if v.mutate != nil {
		v.mutate(&stop)
	}
	return stop, nil
}

type retirementCellFixture struct {
	store     *state.MemStore
	p         runtimequota.CapacityPermit
	authority state.RegionalRuntimeCapacityAuthority
	signer    runtimequota.Signer
}

func newRetirementCell(t *testing.T, issued bool) retirementCellFixture {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "retirement-wire@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{ID: uuid.NewString(), AccountID: acct.ID, Slug: "retirement-wire", Type: state.AppTypeApp, Status: state.AppActive, Runtime: "node22", RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := runtimequota.NewSigner(uuid.NewString(), private)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := runtimequota.NewTrust([]runtimequota.Anchor{{Role: runtimequota.Directory, IssuerID: directory.IssuerID(), Public: pub}})
	if err != nil {
		t.Fatal(err)
	}
	policy, _ := api.RuntimeQuotaPolicyForPlan(api.PlanPro)
	b := runtimequota.Binding{CellID: uuid.NewString(), Region: "eu-fsn", CellGeneration: 1, RegionalAccountID: uuid.MustParse(acct.ID).String(), RegionalAppID: app.ID, RegionalAppSlug: app.Slug, APIBase: "https://cell.example.com"}
	g := runtimequota.Grant{IssuerID: directory.IssuerID(), GlobalAppID: uuid.NewString(), OwnerID: uuid.NewString(), AllocationID: uuid.NewString(), Generation: 1, Binding: b, Policy: policy, Concurrency: 2, IssuedAtMicros: 1}
	e, err := directory.SignGrant(g)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (state.RegionalRuntimeQuotaAuthority{Store: store, Directories: trust, Binding: b}).AcceptGrant(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	p := runtimequota.CapacityPermit{Grant: g, ID: uuid.NewString(), InstanceID: uuid.NewString(), RunID: uuid.NewString(), DeploymentID: uuid.NewString(), NodeID: uuid.NewString(), HostEpochID: uuid.NewString(), Kind: runtimequota.CapacityWarm}
	if _, err = store.ReserveRegionalRuntimeCapacity(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if issued {
		if _, err = store.IssueRegionalRuntimeCapacity(t.Context(), app.ID, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	pub, private, err = ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := runtimequota.NewSigner(b.CellID, private)
	if err != nil {
		t.Fatal(err)
	}
	trust, err = runtimequota.NewTrust([]runtimequota.Anchor{{Role: runtimequota.Cell, IssuerID: b.CellID, Public: pub}})
	if err != nil {
		t.Fatal(err)
	}
	return retirementCellFixture{store: store, p: p, signer: signer, authority: state.RegionalRuntimeCapacityAuthority{Store: store, Cells: trust}}
}

func newRetirementWire(t *testing.T, f retirementCellFixture, v vmmdgrpc.VmmdAPI, clientCN, serverCN string, plaintext bool, interceptor grpc.UnaryServerInterceptor) (*sched.VMMRouter, vmmdpb.VmmdClient) {
	t.Helper()
	var serverTLS, clientTLS *tls.Config
	if !plaintext {
		dir := t.TempDir()
		ca, key, err := pki.EnsureCA(dir, false)
		if err != nil {
			t.Fatal(err)
		}
		serverRole := pki.Role{CommonName: serverCN, Kind: pki.KindServer, Directory: "native", Filename: "server", AltNames: pki.LocalDevSANs()}
		clientRole := pki.Role{CommonName: clientCN, Kind: pki.KindClient, Directory: "cell", Filename: "client"}
		for _, role := range []pki.Role{serverRole, clientRole} {
			if err = pki.EnsureLeaf(dir, role, ca, key, false); err != nil {
				t.Fatal(err)
			}
		}
		caPath, _ := pki.CARoot(dir)
		certPath, keyPath := pki.LeafPaths(dir, serverRole)
		serverTLS, err = wire.LoadServerTLSConfig(certPath, keyPath, caPath)
		if err != nil {
			t.Fatal(err)
		}
		certPath, keyPath = pki.LeafPaths(dir, clientRole)
		clientTLS, err = wire.LoadClientTLSConfig(certPath, keyPath, caPath)
		if err != nil {
			t.Fatal(err)
		}
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	opts := wire.ServerCredsOrEmpty(serverTLS)
	if interceptor != nil {
		opts = append(opts, grpc.UnaryInterceptor(interceptor))
	}
	srv := grpc.NewServer(opts...)
	vmmdgrpc.New(v, wire.NewOpsMetrics("regional_retirement_test"), "1.10.0", nil).WithNodeID(f.p.NodeID).Register(srv)
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close(); <-serveDone })
	// Explicit insecure dial exercises refusal at the new handler boundary.
	var conn *grpc.ClientConn
	if plaintext {
		conn, err = grpc.NewClient("passthrough:///"+lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		conn, err = wire.DialContext(t.Context(), "tcp://"+lis.Addr().String(), clientTLS)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	r := sched.NewVMMRouter(nil, nil, clientTLS).WithRegionalRuntimePeerResolver(retirementPeerResolver{"native-a": f.p.NodeID, "native-b": uuid.NewString()})
	r.SetClient(f.p.NodeID, sched.NewVMMClient(conn))
	return r, vmmdpb.NewVmmdClient(conn)
}

func TestRegionalRuntimeRetirementAuthenticatedWireAndLostReply(t *testing.T) {
	f := newRetirementCell(t, true)
	compact := strings.ReplaceAll(f.p.InstanceID, "-", "")
	v := &retirementWireVMM{fakeVMM: &fakeVMM{}, store: f.store, wantInstance: compact}
	var lost atomic.Bool
	interceptor := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		if err == nil && !lost.Swap(true) {
			return nil, status.Error(codes.Unavailable, "reply lost after native verification")
		}
		return resp, err
	}
	r, _ := newRetirementWire(t, f, v, "schedd.faas", "native-a", false, interceptor)
	owner := sched.RegionalRuntimeRetirement{Authority: f.authority, Signer: f.signer, Router: r}
	first := uuid.NewString()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := owner.Retire(ctx, compact, f.p, first); err == nil {
		t.Fatal("lost reply released hold")
	}
	retained, err := f.store.RegionalRuntimeCapacity(ctx, f.p.Grant.Binding.RegionalAppID, f.p.ID)
	if err != nil || retained.ReleasedAt != nil || retained.StopEnvelope != nil || retained.FenceID != first {
		t.Fatal("lost reply changed capacity", err)
	}
	if err = f.store.DeleteApp(ctx, f.p.Grant.Binding.RegionalAppID); err != nil {
		t.Fatal(err)
	}
	closed, err := owner.Retire(ctx, compact, f.p, uuid.NewString())
	if err != nil || closed.ReleasedAt == nil || closed.FenceID != first || closed.StopEnvelope == nil || v.calls.Load() != 2 {
		t.Fatalf("historical retry: %+v %v", closed, err)
	}
	stop, err := f.authority.Cells.VerifyCapacityStop(*closed.StopEnvelope)
	if err != nil || stop.Permit != f.p || stop.FenceID != first {
		t.Fatal("cell signed changed evidence", err)
	}
	replay, err := owner.Retire(ctx, compact, f.p, uuid.NewString())
	if err != nil || !reflect.DeepEqual(replay, closed) || v.calls.Load() != 2 {
		t.Fatal("accepted release was not replayed", err)
	}
}

func TestRegionalRuntimeRetirementWireRefusalsRetainCapacity(t *testing.T) {
	for _, name := range []string{"wrong-client-role", "wrong-server-node", "plaintext", "legacy-native", "different-host-fence", "wrong-permit", "malformed-response", "wrong-instance", "missing-resolver", "untrusted-signer", "native-uncertain"} {
		t.Run(name, func(t *testing.T) {
			f := newRetirementCell(t, true)
			v := &retirementWireVMM{fakeVMM: &fakeVMM{}, store: f.store}
			clientCN, serverCN := "schedd.faas", "native-a"
			plaintext := false
			var native vmmdgrpc.VmmdAPI = v
			var interceptor grpc.UnaryServerInterceptor
			switch name {
			case "wrong-client-role":
				clientCN = "gatewayd.faas"
			case "wrong-server-node":
				serverCN = "native-b"
			case "plaintext":
				plaintext = true
			case "legacy-native":
				native = &fakeVMM{}
			case "different-host-fence":
				v.firstFence = uuid.NewString()
			case "wrong-permit":
				v.mutate = func(s *runtimequota.CapacityStop) { s.Permit.RunID = uuid.NewString() }
			case "malformed-response", "wrong-instance":
				interceptor = func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
					resp, err := h(ctx, req)
					if err == nil {
						r := resp.(*vmmdpb.RetireRegionalRuntimeResponse)
						if name == "malformed-response" {
							r.Stop = append([]byte(" "), r.Stop...)
						} else {
							r.Instance = uuid.NewString()
						}
					}
					return resp, err
				}
			case "native-uncertain":
				v.mutate = func(s *runtimequota.CapacityStop) { s.CountedRuntimes = 1 }
			}
			r, _ := newRetirementWire(t, f, native, clientCN, serverCN, plaintext, interceptor)
			owner := sched.RegionalRuntimeRetirement{Authority: f.authority, Signer: f.signer, Router: r}
			if name == "missing-resolver" {
				r.WithRegionalRuntimePeerResolver(nil)
			}
			if name == "untrusted-signer" {
				owner.Authority.Cells = runtimequota.Trust{}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if _, err := owner.Retire(ctx, f.p.InstanceID, f.p, uuid.NewString()); err == nil {
				t.Fatal("unauthenticated/uncertain evidence released capacity")
			}
			retained, err := f.store.RegionalRuntimeCapacity(ctx, f.p.Grant.Binding.RegionalAppID, f.p.ID)
			if err != nil || retained.ReleasedAt != nil || retained.Stop != nil || retained.StopEnvelope != nil {
				t.Fatal("refusal released hold", err)
			}
			if (name == "wrong-client-role" || name == "plaintext" || name == "missing-resolver" || name == "untrusted-signer") && v.calls.Load() != 0 {
				t.Fatal("refusal reached native work")
			}
		})
	}
}

func TestRegionalRuntimeRetirementRequestValidationBeforeNative(t *testing.T) {
	f := newRetirementCell(t, true)
	v := &retirementWireVMM{fakeVMM: &fakeVMM{}, store: f.store}
	_, cli := newRetirementWire(t, f, v, "schedd.faas", "native-a", false, nil)
	raw, err := json.Marshal(f.p)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*vmmdpb.RetireRegionalRuntimeRequest){func(r *vmmdpb.RetireRegionalRuntimeRequest) { r.Permit = append([]byte(" "), r.Permit...) }, func(r *vmmdpb.RetireRegionalRuntimeRequest) { r.Instance = uuid.NewString() }, func(r *vmmdpb.RetireRegionalRuntimeRequest) { r.FenceId = "" }, func(r *vmmdpb.RetireRegionalRuntimeRequest) {
		p := f.p
		p.NodeID = uuid.NewString()
		r.Permit, _ = json.Marshal(p)
	}} {
		req := &vmmdpb.RetireRegionalRuntimeRequest{Instance: f.p.InstanceID, Permit: append([]byte(nil), raw...), FenceId: uuid.NewString()}
		change(req)
		if _, err = cli.RetireRegionalRuntime(t.Context(), req); status.Code(err) != codes.InvalidArgument {
			t.Fatal("malformed request accepted", err)
		}
	}
	if v.calls.Load() != 0 {
		t.Fatal("bad ownership reached native runtime")
	}
}

func TestRegionalRuntimeRetirementUnissuedNeedsNoHost(t *testing.T) {
	f := newRetirementCell(t, false)
	r := sched.NewVMMRouter(nil, nil, nil).WithRegionalRuntimePeerResolver(retirementPeerResolver{})
	owner := sched.RegionalRuntimeRetirement{Authority: f.authority, Signer: f.signer, Router: r}
	closed, err := owner.Retire(t.Context(), f.p.InstanceID, f.p, uuid.NewString())
	if err != nil || closed.ReleasedAt == nil || closed.Stop != nil || closed.StopEnvelope != nil {
		t.Fatal("unissued cancellation required a host", err)
	}
}
