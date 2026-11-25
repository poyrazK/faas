package pgproxy

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
)

func TestParseEndpoint(t *testing.T) {
	const id = "0b9a4f1e-6a33-4c9e-9a63-3a1f0f2c9d11"
	for _, tc := range []struct {
		label string
		want  endpoint
		ok    bool
	}{
		{id, endpoint{databaseID: id}, true},
		{id + "-pooler", endpoint{databaseID: id, pooled: true}, true},
		{id + "-ro", endpoint{databaseID: id, readOnly: true}, true},
		{id + "-ro-pooler", endpoint{databaseID: id, pooled: true, readOnly: true}, true},
		{strings.ToUpper(id) + "-RO", endpoint{databaseID: id, readOnly: true}, true},
		{id + "-replica", endpoint{databaseID: id, readOnly: true, replicaOnly: true}, true},
		{id + "-replica-pooler", endpoint{databaseID: id, pooled: true, readOnly: true, replicaOnly: true}, true},
		{id + "-ro-replica", endpoint{}, false},
		{id + "-pooler-ro", endpoint{}, false},
		{id + "-ro-ro", endpoint{}, false},
		{"not-a-database-ro", endpoint{}, false},
	} {
		got, ok := parseEndpoint(tc.label)
		if ok != tc.ok || got != tc.want {
			t.Errorf("parseEndpoint(%q) = %+v, %v; want %+v, %v", tc.label, got, ok, tc.want, tc.ok)
		}
	}
}

// replicaBackend is a fake PostgreSQL server: "ok" logs a session in,
// "refuse" closes it at once (a standby that serves no sessions), "stall"
// never answers. It records each session's startup parameters.
type replicaBackend struct {
	mu      sync.Mutex
	startup []map[string]string
}

func (b *replicaBackend) serve(t *testing.T, behavior string) io.ReadWriteCloser {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	go func() {
		defer server.Close()
		if behavior == "refuse" {
			return
		}
		be := pgproto3.NewBackend(server, server)
		msg, err := be.ReceiveStartupMessage()
		if err != nil {
			return
		}
		b.mu.Lock()
		b.startup = append(b.startup, msg.(*pgproto3.StartupMessage).Parameters)
		b.mu.Unlock()
		if behavior == "stall" {
			_, _ = io.Copy(io.Discard, server)
			return
		}
		be.Send(&pgproto3.AuthenticationOk{})
		be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		if be.Flush() != nil {
			return
		}
		_, _ = io.Copy(io.Discard, server)
	}()
	return client
}

func TestReadOnlyEndpointLogsInOnTheReplicaOrThePrimary(t *testing.T) {
	primary := Backend{Target: "primary-target", Node: "db-a"}
	withReplica := Backend{Target: "primary-target", Node: "db-a", ReplicaTarget: "standby-target", ReplicaNode: "db-b"}
	replica := Backend{Target: "standby-target", Node: "db-b"}
	for _, tc := range []struct {
		name     string
		woken    Backend
		readOnly bool
		replica  string // the replica's behavior; "dial" fails its dial
		want     Backend
	}{
		{"read-only to a serving replica", withReplica, true, "ok", replica},
		{"read-only without a replica", primary, true, "", primary},
		{"replica dial fails", withReplica, true, "dial", primary},
		{"replica serves no sessions", withReplica, true, "refuse", primary},
		{"replica does not answer", withReplica, true, "stall", primary},
		{"read-write ignores the replica", withReplica, false, "ok", primary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var backends = map[string]*replicaBackend{"primary-target": {}, "standby-target": {}}
			dial := setupDialer(func(_ context.Context, b Backend, id string) (io.ReadWriteCloser, error) {
				if id != "db-id" || b.ReplicaTarget != "" {
					t.Errorf("dialed %+v for %q; want a plain backend for db-id", b, id)
				}
				if b.Target == "standby-target" {
					if tc.replica == "dial" {
						return nil, errors.New("no route to the standby")
					}
					return backends[b.Target].serve(t, tc.replica), nil
				}
				return backends[b.Target].serve(t, "ok"), nil
			})
			p, err := New(Config{TLS: selfSignedTLS(t), HostSuffix: testSuffix, DecoySecret: []byte(strings.Repeat("k", 32)), ReplicaLoginTimeout: 100 * time.Millisecond},
				&mapCredentials{}, setupWaker{}, dial, nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			l, err := p.loginEndpoint(ctx, tc.woken, tc.readOnly, "db-id", map[string]string{"user": "app", "database": "app"}, nil, nil)
			if err != nil {
				t.Fatalf("loginEndpoint = %v", err)
			}
			defer l.rw.Close()
			if err := l.finish(); err != nil {
				t.Fatalf("setup deadline = %v", err)
			}
			if l.backend != tc.want {
				t.Fatalf("session went to %+v, want %+v", l.backend, tc.want)
			}
			// The session's startup is the client's, read-only on a
			// read-only endpoint wherever it went.
			got := backends[tc.want.Target]
			got.mu.Lock()
			defer got.mu.Unlock()
			if len(got.startup) == 0 {
				t.Fatal("the chosen backend saw no startup")
			}
			params := got.startup[len(got.startup)-1]
			if ro := params["default_transaction_read_only"]; (ro == "on") != tc.readOnly || params["user"] != "app" {
				t.Fatalf("startup = %v; want read-only %v for user app", params, tc.readOnly)
			}
		})
	}
}

func TestScheddWakerNamesTheReplica(t *testing.T) {
	for _, tc := range []struct {
		name          string
		replicaTarget string
		standbyNode   string
		wantReplica   bool
	}{
		{"serving standby", "standby-target", "db-b", true},
		{"older schedd names no replica", "", "db-b", false},
		{"replica without its node", "standby-target", "", false},
		{"standby on the primary's node", "standby-target", "db-a", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := backendFromRuntime("primary-target", "db-a", tc.replicaTarget, tc.standbyNode)
			if _, ok := b.replica(); ok != tc.wantReplica {
				t.Fatalf("replica named = %v, want %v (%+v)", ok, tc.wantReplica, b)
			}
			if b.primary() != (Backend{Target: "primary-target", Node: "db-a"}) {
				t.Fatalf("primary = %+v", b.primary())
			}
		})
	}
}

type replicaRouteFunc func(context.Context, string) (Backend, *int64, time.Time, error)

func (f replicaRouteFunc) ReplicaRoute(ctx context.Context, id string) (Backend, *int64, time.Time, error) {
	return f(ctx, id)
}

func (f replicaRouteFunc) Wake(context.Context, string) (Backend, error) {
	panic("a replica-only client attempted to wake a primary")
}

func (f replicaRouteFunc) Touch(context.Context, string, int64) error { return nil }

func TestStrictReplicaCheckout(t *testing.T) {
	zero := int64(0)
	backlog := int64(1)
	lag, observed := &zero, time.Now()
	replica := Backend{Target: "standby-target", Node: "db-b"}
	waker := replicaRouteFunc(func(context.Context, string) (Backend, *int64, time.Time, error) {
		return replica, lag, observed, nil
	})
	ctx := t.Context()
	dialCount := 0
	refuse := false
	backend := &replicaBackend{}
	p, err := New(Config{
		TLS: selfSignedTLS(t), HostSuffix: testSuffix, DecoySecret: []byte(strings.Repeat("k", 32)),
		ReplicaMaxReplayLagBytes: &zero,
	}, &mapCredentials{}, waker, setupDialer(func(_ context.Context, target Backend, _ string) (io.ReadWriteCloser, error) {
		dialCount++
		if !target.replicaOnly || target.Node != replica.Node || target.Target != replica.Target {
			t.Errorf("dialed %+v with standby %+v", target, replica)
		}
		if refuse {
			return nil, errors.New("standby lost")
		}
		return backend.serve(t, "ok"), nil
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	pl := p.pools.join(poolKey{databaseID: "db-id", readOnly: true, replicaOnly: true}, nil, nil, map[string]string{"user": "reader"})
	initial, err := pl.acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pl.put(initial, true)
	for _, tc := range []struct {
		lag *int64
		at  time.Time
	}{
		{nil, time.Now()},
		{&backlog, time.Now()},
		{&zero, time.Now().Add(-2 * replicaMaxSampleAge)},
	} {
		lag, observed = tc.lag, tc.at
		if _, err := pl.acquire(ctx); !errors.Is(err, errReplicaUnavailable) {
			t.Fatalf("checkout with unknown, excess or expired replay evidence: %v", err)
		}
	}
	lag, observed = &zero, time.Now()
	got, err := pl.acquire(ctx)
	if err != nil || got != initial || dialCount != 1 {
		t.Fatalf("caught-up idle connection: got %p, %v after %d dials", got, err, dialCount)
	}
	pl.put(got, true)
	replica = Backend{Target: "next-standby", Node: "db-c"}
	refuse = true
	if _, err := pl.acquire(ctx); !errors.Is(err, errReplicaUnavailable) || dialCount != 2 {
		t.Fatalf("after standby changed and refused login: %v, %d dials", err, dialCount)
	}
	refuse = false
	got, err = pl.acquire(ctx)
	if err != nil || got == initial || dialCount != 3 {
		t.Fatalf("replacement standby: got %p, %v after %d dials", got, err, dialCount)
	}
	pl.put(got, false)
}
