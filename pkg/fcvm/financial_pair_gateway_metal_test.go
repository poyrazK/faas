//go:build linux && metal

package fcvm_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/wire"
)

type financialPairMeasurement struct {
	BlockedPayload                                                   map[string]uint64
	InterfaceIndex                                                   int
	Case                                                             string                        `json:"case"`
	GatewayHost                                                      fcvm.FinancialPairTrafficHost `json:"gateway_host"`
	OwnerHost                                                        fcvm.FinancialPairTrafficHost `json:"owner_host"`
	Node, Instance, Incarnation, LeaseID, Veth, RPCTarget            string
	Payload, Before, After, Funded                                   uint64
	RetryPayload, StableAfter                                        uint64
	ReservedMillicents, CommittedMillicents, LimitMillicents         int64
	EgressReserved, EgressCommitted, ReceiptRevision, ReplayRevision int64
	UnconfirmedLeases                                                int
	Settled                                                          bool
	PhysicalAbsent, DatabaseUnavailable, CounterAvailable            bool
	ObservedAt, FundedUntil                                          time.Time
}

func (g *financialPairGateway) measurement(t *testing.T, name string, payload uint64, before, after fcvm.FinancialPairTrafficEvent, absent, offline bool) financialPairMeasurement {
	t.Helper()
	lease := g.fixture.Lease()
	balance, limit := g.fixture.Balance(t)
	ready := g.fixture.Ready()
	value := financialPairMeasurement{Case: name, GatewayHost: fcvm.FinancialPairTrafficHostFacts(), OwnerHost: ready.Host,
		Node: g.fixture.Node(), Instance: g.fixture.Instance(), Incarnation: ready.Identity.Incarnation, InterfaceIndex: before.InterfaceIndex,
		LeaseID: lease.ID, Veth: before.Veth, RPCTarget: ready.RPCTarget, Payload: payload, Before: before.InterfaceBytes, After: after.InterfaceBytes,
		Funded: uint64(lease.EgressGranted), ReservedMillicents: balance.ReservedMillicents, CommittedMillicents: balance.CommittedMillicents,
		LimitMillicents: limit, PhysicalAbsent: absent, DatabaseUnavailable: offline, CounterAvailable: after.CounterAvailable,
		EgressReserved: balance.Egress.ReservedBytes, EgressCommitted: balance.Egress.CommittedBytes,
		UnconfirmedLeases: balance.UnconfirmedLeases, ReceiptRevision: lease.Revision, Settled: lease.SettledAt != nil,
		ObservedAt: time.Now().UTC(), FundedUntil: lease.FundedUntil}
	if value.GatewayHost.MachineID == value.OwnerHost.MachineID || value.GatewayHost.BootID == value.OwnerHost.BootID || ready.RPCTarget == "" || lease.EgressGranted <= 0 {
		t.Fatal("cross-host traffic has no distinct native owner or funded identity")
	}
	return value
}

func logFinancialPairMeasurement(t *testing.T, value financialPairMeasurement) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("financial-pair-traffic:%s", encoded)
}

func (g *financialPairGateway) measured(t *testing.T, name string, action func(context.Context) ([]byte, error)) {
	t.Helper()
	before := g.fixture.Call(t, "status")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	body, err := action(ctx)
	if err != nil || len(body) == 0 {
		t.Fatalf("%s cross-host response: bytes=%d error=%v", name, len(body), err)
	}
	after := g.fixture.Call(t, "status")
	if !before.CounterAvailable || !after.CounterAvailable || before.Veth != after.Veth || before.InterfaceIndex != after.InterfaceIndex || after.InterfaceBytes < before.InterfaceBytes {
		t.Fatal("canonical interface identity/counter was not retained")
	}
	value := g.measurement(t, name, uint64(len(body)), before, after, false, false)
	delta := value.After - value.Before
	if delta < value.Payload || delta >= 2*value.Payload || value.After > value.Funded {
		t.Fatalf("unmetered, duplicated or unfunded payload: %+v", value)
	}
	logFinancialPairMeasurement(t, value)
}

func (g *financialPairGateway) checkedHTTP(ctx context.Context, path string, port int) ([]byte, error) {
	response := g.http(ctx, path)
	expected := bytes.Repeat([]byte{byte(port % 251)}, pairPayloadSize)
	if response.err == nil && (response.port != fmt.Sprint(port) || !bytes.Equal(response.body, expected) || response.chunks < 2) {
		response.err = fmt.Errorf("wrong port, body or stream: port=%s bytes=%d chunks=%d", response.port, len(response.body), response.chunks)
	}
	return response.body, response.err
}

func TestMetalFinancialPairGatewayProtocols(t *testing.T) {
	g := newFinancialPairGateway(t)
	if !t.Run("http_stream", func(t *testing.T) {
		g.measured(t, "http_stream", func(ctx context.Context) ([]byte, error) {
			responses := make(chan pairResponse, 4)
			var group sync.WaitGroup
			for range 4 {
				group.Go(func() { responses <- g.http(ctx, "/") })
			}
			group.Wait()
			close(responses)
			var body []byte
			for response := range responses {
				if response.err != nil {
					return body, fmt.Errorf("concurrent HTTP stream: %w", response.err)
				}
				if !bytes.Equal(response.body, bytes.Repeat([]byte{byte(9000 % 251)}, pairPayloadSize)) || response.port != "9000" || response.chunks < 2 {
					return body, fmt.Errorf("concurrent HTTP stream: bytes=%d chunks=%d", len(response.body), response.chunks)
				}
				body = append(body, response.body...)
			}
			return body, nil
		})
	}) {
		return
	}
	if !t.Run("custom_main", func(t *testing.T) {
		g.measured(t, "custom_main", func(ctx context.Context) ([]byte, error) { return g.checkedHTTP(ctx, "/", 9000) })
	}) {
		return
	}
	if !t.Run("sidecar_8080", func(t *testing.T) {
		g.measured(t, "sidecar_8080", func(ctx context.Context) ([]byte, error) { return g.checkedHTTP(ctx, "/sidecar8080", 8080) })
	}) {
		return
	}
	if !t.Run("sidecar_9001", func(t *testing.T) {
		g.measured(t, "sidecar_9001", func(ctx context.Context) ([]byte, error) { return g.checkedHTTP(ctx, "/sidecar9001", 9001) })
	}) {
		return
	}
	if !t.Run("h2c_grpc", func(t *testing.T) { g.measured(t, "h2c_grpc", g.grpcPayload) }) {
		return
	}
	if !t.Run("upgrade", func(t *testing.T) { g.measured(t, "upgrade", g.upgrade) }) {
		return
	}
	if !t.Run("tcp", func(t *testing.T) { g.measured(t, "tcp", g.tcp) }) {
		return
	}
	t.Run("udp", func(t *testing.T) { g.measured(t, "udp", g.udp) })
}

func TestMetalFinancialPairGatewayFailureBounds(t *testing.T) {
	g := newFinancialPairGateway(t)
	// Close real retained SQL sessions and keep the observer pool connected.
	g.fixture.DatabaseAvailable(false)
	lease := g.fixture.Lease() // join the final possible database grant
	if !t.Run("exhaustion", func(t *testing.T) { g.exhaustion(t) }) {
		return
	}
	if !t.Run("private_isolation", func(t *testing.T) { g.privateIsolation(t) }) {
		return
	}
	t.Run("deadline", func(t *testing.T) { g.deadline(t, lease) })
}

func (g *financialPairGateway) exhaustion(t *testing.T) {
	t.Helper()
	before := g.fixture.Call(t, "status")
	responses := make(chan uint64, 16)
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			var total uint64
			for range 8 {
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				response := g.http(ctx, "/")
				cancel()
				total += uint64(len(response.body))
			}
			responses <- total
		})
	}
	group.Wait()
	close(responses)
	var payload uint64
	for n := range responses {
		payload += n
	}
	beforeRetry := g.fixture.Call(t, "status")
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	response := g.http(ctx, "/")
	blocked := make(map[string]uint64)
	for _, probe := range []struct {
		name string
		call func(context.Context) ([]byte, error)
	}{{"h2c_grpc", g.grpcPayload}, {"upgrade", g.upgrade}, {"tcp", g.tcp}, {"udp", g.udp}} {
		probeCtx, probeCancel := context.WithTimeout(t.Context(), time.Second)
		body, _ := probe.call(probeCtx)
		probeCancel()
		blocked[probe.name] = uint64(len(body))
		if len(body) != 0 {
			t.Fatalf("%s delivered %d bytes after quota exhaustion", probe.name, len(body))
		}
	}
	after := g.fixture.Call(t, "status")
	if !before.CounterAvailable || !after.CounterAvailable || payload == 0 || after.InterfaceBytes < before.InterfaceBytes || uint64(payload) > after.InterfaceBytes-before.InterfaceBytes || len(response.body) != 0 || after.InterfaceBytes != beforeRetry.InterfaceBytes || after.Physical == 0 {
		t.Fatalf("kernel quota did not bound live remote traffic: before=%+v after=%+v payload=%d retry=%d", before, after, payload, len(response.body))
	}
	value := g.measurement(t, "exhaustion", payload, before, after, false, true)
	value.RetryPayload, value.StableAfter = uint64(len(response.body)), beforeRetry.InterfaceBytes
	value.BlockedPayload = blocked
	if value.After > value.Funded {
		t.Fatal("cross-host egress exceeded issued credits")
	}
	logFinancialPairMeasurement(t, value)
}

func (g *financialPairGateway) deadline(t *testing.T, lease financial.ComputeLease) {
	t.Helper()
	before := g.fixture.Call(t, "status")
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	var after fcvm.FinancialPairTrafficEvent
	for time.Now().Before(lease.FundedUntil.Add(time.Second)) {
		after = g.fixture.Call(t, "status")
		if after.Physical == 0 && after.ResourcesRemoved {
			break
		}
		select {
		case <-t.Context().Done():
			t.Fatal("deadline observation cancelled")
		case <-tick.C:
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	response := g.http(ctx, "/")
	if after.Physical != 0 || !after.ResourcesRemoved || after.CounterAvailable || len(response.body) != 0 || g.fixture.Lease().SettledAt != nil {
		t.Fatal("deadline failed to retire physical authority while preserving uncertain credits")
	}
	value := g.measurement(t, "deadline", 0, before, after, true, true)
	logFinancialPairMeasurement(t, value)
}

func (g *financialPairGateway) privateIsolation(t *testing.T) {
	t.Helper()
	before := g.fixture.Call(t, "status")
	// The caller is the platform's authorized service hop. This test covers
	// its transport marker; the ServiceProxy authorization suite stays required.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.serveHTTP(w, r.WithContext(wire.WithPrivateForward(r.Context())))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	response := g.httpAt(ctx, server.URL+"/")
	after := g.fixture.Call(t, "status")
	if response.err != nil || !bytes.Equal(response.body, bytes.Repeat([]byte{byte(9000 % 251)}, pairPayloadSize)) || !before.CounterAvailable || !after.CounterAvailable || before.InterfaceBytes != after.InterfaceBytes {
		t.Fatalf("private transport failed or added public bytes: payload=%d counter=%d -> %d error=%v", len(response.body), before.InterfaceBytes, after.InterfaceBytes, response.err)
	}
	logFinancialPairMeasurement(t, g.measurement(t, "private_isolation", uint64(len(response.body)), before, after, false, true))
}

func TestMetalFinancialPairGatewayOwnerRestart(t *testing.T) {
	g := newFinancialPairGateway(t)
	if !t.Run("before_crash", func(t *testing.T) {
		g.measured(t, "before_crash", func(ctx context.Context) ([]byte, error) { return g.checkedHTTP(ctx, "/", 9000) })
	}) {
		return
	}
	old := g.fixture.Lease()
	g.fixture.Restart(t, false)
	g.fixture.Call(t, "recover")
	g.fixture.PublishTarget(t)
	g.nodes.Evict(g.fixture.Node())
	if !t.Run("retired_attempt", func(t *testing.T) {
		before := g.fixture.Call(t, "status")
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		response := g.http(ctx, "/")
		after := g.fixture.Call(t, "status")
		final := g.fixture.Lease()
		if final.ID != old.ID || final.SettledAt == nil || after.Physical != 0 || !after.ResourcesRemoved || len(response.body) != 0 {
			t.Fatal("restart revived the retired physical attempt or lost its receipt")
		}
		g.fixture.Call(t, "recover")
		replay := g.fixture.Lease()
		if replay.Revision != final.Revision || replay.Committed != final.Committed || !replay.SettledAt.Equal(*final.SettledAt) {
			t.Fatal("restart settled funding twice")
		}
		value := g.measurement(t, "retired_attempt", 0, before, after, true, false)
		value.ReplayRevision = replay.Revision
		logFinancialPairMeasurement(t, value)
	}) {
		return
	}
	g.fixture.Restart(t, true)
	g.fixture.PublishTarget(t)
	g.nodes.Evict(g.fixture.Node())
	g.fixture.Call(t, "wake")
	t.Run("new_attempt", func(t *testing.T) {
		if lease := g.fixture.Lease(); lease.ID == old.ID || lease.InstanceID == old.InstanceID {
			t.Fatal("replacement reused old funding authority")
		}
		g.measured(t, "new_attempt", func(ctx context.Context) ([]byte, error) { return g.checkedHTTP(ctx, "/", 9000) })
	})
}
