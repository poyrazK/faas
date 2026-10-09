package state

// adr: 711

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/gateway/edgetopology"
	"github.com/onebox-faas/faas/pkg/gateway/ingress"
)

type nativeBindingFixture struct {
	s       *PgStore
	pool    *pgxpool.Pool
	review  RuntimeUpgradeNativePublicStartupReview
	member  RuntimeUpgradePublicEdgeMember
	gateway RuntimeUpgradeGatewayRoster
	public  RuntimeUpgradePublicEdgeRoster
	calls   atomic.Int32
}

func newNativeBindingFixture(t *testing.T) *nativeBindingFixture {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	f := &nativeBindingFixture{s: NewPgStore(pool), pool: pool}
	var err error
	f.gateway, err = f.s.ReviewRuntimeUpgradeGatewayRoster(t.Context(), "", []RuntimeUpgradeGatewayMember{{SlotID: uuid.NewString(), SessionID: uuid.NewString()}})
	if err != nil {
		t.Fatal(err)
	}
	f.member = RuntimeUpgradePublicEdgeMember{SlotID: uuid.NewString(), SessionID: uuid.NewString(), ConfigSHA256: strings.Repeat("a", 64)}
	f.public, err = f.s.ReviewRuntimeUpgradePublicEdgeRoster(t.Context(), "", f.gateway.Revision, strings.Repeat("b", 64), []RuntimeUpgradePublicEdgeMember{f.member})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.RecordRuntimeUpgradePublicEdgeGuard(t.Context(), f.member); err != nil {
		t.Fatal(err)
	}
	f.review = RuntimeUpgradeNativePublicStartupReview{GatewayRevision: f.gateway.Revision, PublicRevision: f.public.Revision, Selection: edgetopology.NativeStartupReview{
		Binding: edgetopology.Binding{Address: "127.0.0.1:8080", Edge: ingress.PublicEdgeIdentity{SlotID: f.member.SlotID, SessionID: f.member.SessionID, ConfigSHA256: f.member.ConfigSHA256}},
		Scope: edgetopology.NativeScopeReview{Host: edgetopology.NativeHostReview{MachineID: strings.Repeat("1", 32), BootID: uuid.NewString(), PIDNamespace: "pid:[10]", NetNamespace: "net:[20]", Addresses: []edgetopology.NativeHostAddress{{IP: "192.0.2.10", Interface: "eth0", Index: 2, Prefix: 24}}},
			Services: []edgetopology.NativeServiceReview{{Unit: "faas-gatewayd-public.service", InvocationID: strings.Repeat("2", 32), PID: 12345, StartTicks: 67890, UID: 998, ExeSHA256: strings.Repeat("d", 64), Cgroup: "/faas-cp.slice/faas-gatewayd-public.service", CgroupInode: 202, TCPListeners: []string{"127.0.0.1:8080", "127.0.0.1:9092"}}}},
	}}
	return f
}

// Test-only synthetic native inventory/issuer exercises the private store seam.
// Public production enrollment always invokes NativeStartupProbe.Observe.
func (f *nativeBindingFixture) collect(_ context.Context, selection edgetopology.NativeStartupReview) (nativeStartupCollection, error) {
	f.calls.Add(1)
	frozen, startup, err := edgetopology.CanonicalNativeStartupReview(selection)
	if err != nil {
		return nativeStartupCollection{}, err
	}
	proof := struct {
		Startup ingress.NativePublicStartup `json:"startup"`
		Nonce   string                      `json:"nonce"`
		Proof   string                      `json:"proof"`
	}{Startup: startup, Nonce: uuid.NewString()}
	raw, err := json.Marshal(proof)
	if err != nil {
		return nativeStartupCollection{}, err
	}
	h := hmac.New(sha256.New, bytes.Repeat([]byte{7}, 32))
	_, _ = h.Write([]byte("gregale/runtime-public-edge/native-startup/response/v1:" + string(raw)))
	proof.Proof = hex.EncodeToString(h.Sum(nil))
	raw, err = json.Marshal(proof)
	if err != nil {
		return nativeStartupCollection{}, err
	}
	s := frozen.Scope.Services[0]
	service := edgetopology.NativeServiceObservation{Review: s, Executable: edgetopology.NativeFileIdentity{Device: 1, Inode: 101, SHA256: s.ExeSHA256}, Cgroup: edgetopology.NativeFileIdentity{Device: 2, Inode: s.CgroupInode}}
	for i, addr := range s.TCPListeners {
		service.Listeners = append(service.Listeners, edgetopology.NativeTCPListener{Address: addr, Inode: uint64(300 + i), FDs: []int{i + 3}})
	}
	before := edgetopology.NativeScopeObservation{Host: frozen.Scope.Host, Services: []edgetopology.NativeServiceObservation{service}, CheckedAt: time.Now().UTC()}
	after := before
	after.CheckedAt = time.Now().UTC()
	record := edgetopology.NativeStartupSnapshot{Review: frozen, Native: []edgetopology.NativeScopeObservation{before, after}, Proof: raw, CheckedAt: time.Now().UTC()}
	envelope, err := json.Marshal(record)
	return nativeStartupCollection{startup: startup, envelope: envelope}, err
}

func (f *nativeBindingFixture) count(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM runtime_upgrade_native_public_startups`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *nativeBindingFixture) replace(t *testing.T) {
	t.Helper()
	m := f.member
	m.SessionID = uuid.NewString()
	r, err := f.s.ReviewRuntimeUpgradePublicEdgeRoster(t.Context(), f.public.Revision, f.gateway.Revision, strings.Repeat("c", 64), []RuntimeUpgradePublicEdgeMember{m})
	if err != nil {
		t.Fatal(err)
	}
	f.public = r
}

func TestPgNativeStartupEnrollmentOwnsBytesAndUnsignedEpochValues(t *testing.T) {
	f := newNativeBindingFixture(t)
	f.review.Selection.Scope.Services[0].StartTicks = ^uint64(0)
	got, err := f.s.recordNativePublicStartup(t.Context(), f.review, f.collect)
	if err != nil || got.Startup.SessionID != f.member.SessionID || got.Startup.Epoch.StartTicks != ^uint64(0) || got.RecordedAt.Before(got.ObservedAt) || f.count(t) != 1 {
		t.Fatal(got, err)
	}
	wantEnvelope, wantReview := slices.Clone(got.Envelope), slices.Clone(got.Review)
	got.Envelope[0], got.Review[0] = '!', '!'
	read, err := NewPgStore(f.pool).RuntimeUpgradeNativePublicStartup(t.Context(), f.member.SlotID, f.member.SessionID)
	if err != nil || !bytes.Equal(read.Envelope, wantEnvelope) || !bytes.Equal(read.Review, wantReview) || read.EnvelopeSHA256 != nativeStartupDigest(wantEnvelope) || read.ReviewSHA256 != nativeStartupDigest(wantReview) {
		t.Fatal("byte alias or restart drift", read, err)
	}
	for _, tc := range []struct{ slot, session string }{{uuid.NewString(), f.member.SessionID}, {f.member.SlotID, uuid.NewString()}} {
		if _, err := f.s.RuntimeUpgradeNativePublicStartup(t.Context(), tc.slot, tc.session); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign lookup returned provenance", err)
		}
	}
}

func TestPgNativeStartupExactRetryKeepsHistoricalProofAfterWithdrawalAndRestart(t *testing.T) {
	f := newNativeBindingFixture(t)
	first, err := f.s.recordNativePublicStartup(t.Context(), f.review, f.collect)
	if err != nil {
		t.Fatal(err)
	}
	f.replace(t)
	// The first response can be lost. Retry uses original selection/revisions
	// without probing the withdrawn host or minting new nonce/time evidence.
	retry := f.review
	retry.Selection.Scope.Services = slices.Clone(f.review.Selection.Scope.Services)
	retry.Selection.Scope.Services[0].TCPListeners = slices.Clone(retry.Selection.Scope.Services[0].TCPListeners)
	slices.Reverse(retry.Selection.Scope.Services[0].TCPListeners)
	got, err := NewPgStore(f.pool).RecordRuntimeUpgradeNativePublicStartup(t.Context(), retry, nil)
	if err != nil || !reflect.DeepEqual(got, first) || f.calls.Load() != 1 {
		t.Fatal("historical retry refreshed or re-probed", got, err)
	}
	var unresolved int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM runtime_upgrade_public_edge_withdrawals w WHERE public_session_id=$1 AND NOT EXISTS(SELECT 1 FROM runtime_upgrade_public_edge_withdrawal_receipts WHERE withdrawal_id=w.id) AND NOT EXISTS(SELECT 1 FROM runtime_upgrade_external_fence_receipts WHERE withdrawal_id=w.id)`, f.member.SessionID).Scan(&unresolved); err != nil || unresolved != 1 {
		t.Fatal("provenance resolved a withdrawal", unresolved, err)
	}
}

func TestPgNativeStartupFirstEnrollmentRejectsStaleForeignAndWithdrawnMembers(t *testing.T) {
	for _, kind := range []string{"guard-absent", "guard-expired", "guard-future", "gateway", "public", "slot", "session", "config", "withdrawn"} {
		t.Run(kind, func(t *testing.T) {
			f := newNativeBindingFixture(t)
			var err error
			switch kind {
			case "guard-absent":
				_, err = f.pool.Exec(t.Context(), `DELETE FROM runtime_upgrade_public_edge_guards`)
			case "guard-expired":
				_, err = f.pool.Exec(t.Context(), `WITH t AS MATERIALIZED(SELECT clock_timestamp() AS at) UPDATE runtime_upgrade_public_edge_guards SET observed_at=t.at-interval '2 minutes',expires_at=t.at-interval '1 minute' FROM t`)
			case "guard-future":
				_, err = f.pool.Exec(t.Context(), `WITH t AS MATERIALIZED(SELECT clock_timestamp() AS at) UPDATE runtime_upgrade_public_edge_guards SET observed_at=t.at+interval '1 hour',expires_at=t.at+interval '61 minutes' FROM t`)
			case "gateway":
				_, err = f.s.ReviewRuntimeUpgradeGatewayRoster(t.Context(), f.gateway.Revision, []RuntimeUpgradeGatewayMember{{SlotID: uuid.NewString(), SessionID: uuid.NewString()}})
			case "public":
				f.review.PublicRevision = uuid.NewString()
			case "slot":
				f.review.Selection.Binding.Edge.SlotID = uuid.NewString()
			case "session":
				f.review.Selection.Binding.Edge.SessionID = uuid.NewString()
			case "config":
				f.review.Selection.Binding.Edge.ConfigSHA256 = strings.Repeat("b", 64)
			case "withdrawn":
				f.replace(t)
				f.review.PublicRevision = f.public.Revision
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := f.s.recordNativePublicStartup(t.Context(), f.review, f.collect)
			if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(got, RuntimeUpgradeNativePublicStartup{}) || f.calls.Load() != 0 || f.count(t) != 0 {
				t.Fatal("ineligible first enrollment collected or persisted", got, err, f.calls.Load())
			}
		})
	}
}

func TestPgNativeStartupCannotRetargetAnyOriginalReviewField(t *testing.T) {
	f := newNativeBindingFixture(t)
	if _, err := f.s.recordNativePublicStartup(t.Context(), f.review, f.collect); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"gateway", "public", "slot", "session", "config", "machine", "boot", "pid", "start", "pidns", "netns", "invocation", "uid", "exe", "cgroup-inode", "address", "host-address"} {
		t.Run(kind, func(t *testing.T) {
			r := f.review
			r.Selection.Scope.Services = slices.Clone(r.Selection.Scope.Services)
			r.Selection.Scope.Services[0].TCPListeners = slices.Clone(r.Selection.Scope.Services[0].TCPListeners)
			r.Selection.Scope.Host.Addresses = slices.Clone(r.Selection.Scope.Host.Addresses)
			s := &r.Selection.Scope.Services[0]
			switch kind {
			case "gateway":
				r.GatewayRevision = uuid.NewString()
			case "public":
				r.PublicRevision = uuid.NewString()
			case "slot":
				r.Selection.Binding.Edge.SlotID = uuid.NewString()
			case "session":
				r.Selection.Binding.Edge.SessionID = uuid.NewString()
			case "config":
				r.Selection.Binding.Edge.ConfigSHA256 = strings.Repeat("b", 64)
			case "machine":
				r.Selection.Scope.Host.MachineID = strings.Repeat("2", 32)
			case "boot":
				r.Selection.Scope.Host.BootID = uuid.NewString()
			case "pid":
				s.PID++
			case "start":
				s.StartTicks++
			case "pidns":
				r.Selection.Scope.Host.PIDNamespace = "pid:[99]"
			case "netns":
				r.Selection.Scope.Host.NetNamespace = "net:[99]"
			case "invocation":
				s.InvocationID = strings.Repeat("3", 32)
			case "uid":
				s.UID++
			case "exe":
				s.ExeSHA256 = strings.Repeat("e", 64)
			case "cgroup-inode":
				s.CgroupInode++
			case "address":
				r.Selection.Binding.Address = "127.0.0.1:8083"
				s.TCPListeners[0] = r.Selection.Binding.Address
			case "host-address":
				r.Selection.Scope.Host.Addresses[0].IP = "192.0.2.11"
			}
			if got, err := f.s.RecordRuntimeUpgradeNativePublicStartup(t.Context(), r, nil); !errors.Is(err, ErrConflict) || len(got.Envelope) != 0 {
				t.Fatal("original enrollment retargeted", kind, got, err)
			}
		})
	}
	if f.count(t) != 1 || f.calls.Load() != 1 {
		t.Fatal("retarget attempt changed history")
	}
}

func TestPgNativeStartupEvidenceFailureCancellationAndLateExpiryPersistNothing(t *testing.T) {
	for _, kind := range []string{"collector-error", "startup", "body", "shape", "cancel", "late-expiry", "nil-probe", "real-collector"} {
		t.Run(kind, func(t *testing.T) {
			f := newNativeBindingFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			collect := func(ctx context.Context, selection edgetopology.NativeStartupReview) (nativeStartupCollection, error) {
				out, err := f.collect(ctx, selection)
				switch kind {
				case "collector-error":
					return out, edgetopology.ErrNativeUnverified
				case "startup":
					out.startup.Epoch.BootID = uuid.NewString()
				case "body":
					out.envelope = append(out.envelope, '\n')
				case "shape":
					out.envelope = []byte(`{"review":{},"native":[],"proof":null}`)
				case "cancel":
					cancel()
				case "late-expiry":
					_, err = f.pool.Exec(ctx, `WITH t AS MATERIALIZED(SELECT clock_timestamp() AS at) UPDATE runtime_upgrade_public_edge_guards SET observed_at=t.at-interval '2 minutes',expires_at=t.at-interval '1 minute' FROM t`)
				}
				return out, err
			}
			var got RuntimeUpgradeNativePublicStartup
			var err error
			switch kind {
			case "nil-probe":
				got, err = f.s.RecordRuntimeUpgradeNativePublicStartup(ctx, f.review, nil)
			case "real-collector":
				probe, probeErr := edgetopology.NewNativeStartupProbe(strings.Repeat("07", 32))
				if probeErr != nil {
					t.Fatal(probeErr)
				}
				got, err = f.s.RecordRuntimeUpgradeNativePublicStartup(ctx, f.review, probe)
			default:
				got, err = f.s.recordNativePublicStartup(ctx, f.review, collect)
			}
			if err == nil || !reflect.DeepEqual(got, RuntimeUpgradeNativePublicStartup{}) || f.count(t) != 0 {
				t.Fatal("failed evidence left partial provenance", got, err)
			}
		})
	}
}

func TestPgNativeStartupConcurrentEnrollmentReturnsOneOriginalRecord(t *testing.T) {
	f := newNativeBindingFixture(t)
	entered, release := make(chan struct{}, 2), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	collect := func(ctx context.Context, selection edgetopology.NativeStartupReview) (nativeStartupCollection, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return f.collect(ctx, selection)
		case <-ctx.Done():
			return nativeStartupCollection{}, ctx.Err()
		}
	}
	type result struct {
		out RuntimeUpgradeNativePublicStartup
		err error
	}
	done := make(chan result, 2)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for range 2 {
		go func() { out, err := f.s.recordNativePublicStartup(ctx, f.review, collect); done <- result{out, err} }()
	}
	for range 2 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("concurrent calls did not reach collection", ctx.Err())
		}
	}
	close(release)
	first, second := <-done, <-done
	if first.err != nil || second.err != nil || !reflect.DeepEqual(first.out, second.out) || f.count(t) != 1 || f.calls.Load() != 2 {
		t.Fatal("racing enrollment replaced evidence", first, second, f.count(t))
	}
}

func TestPgNativeStartupCollectsAndSamplesDatabaseClockAfterHeadLockWait(t *testing.T) {
	f := newNativeBindingFixture(t)
	blocker, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(t.Context()) }()
	if _, err := blocker.Exec(t.Context(), `SELECT revision FROM runtime_upgrade_gateway_roster_head WHERE singleton FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var got RuntimeUpgradeNativePublicStartup
	var recordErr error
	go func() { got, recordErr = f.s.recordNativePublicStartup(t.Context(), f.review, f.collect); close(done) }()
	select {
	case <-done:
		t.Fatal("enrollment crossed locked head", recordErr)
	case <-time.After(40 * time.Millisecond):
	}
	if f.calls.Load() != 0 {
		t.Fatal("collected before lock wait ended")
	}
	var releasedAt time.Time
	if err := blocker.QueryRow(t.Context(), `SELECT clock_timestamp()`).Scan(&releasedAt); err != nil {
		t.Fatal(err)
	}
	if err := blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	<-done
	if recordErr != nil || got.ObservedAt.Before(releasedAt) || got.RecordedAt.Before(got.ObservedAt) {
		t.Fatal("database clock sampled before wait", got, releasedAt, recordErr)
	}
}

func TestPgNativeStartupSchemaBlocksMutationTruncateAndRawEvidenceMismatch(t *testing.T) {
	f := newNativeBindingFixture(t)
	if _, err := f.s.recordNativePublicStartup(t.Context(), f.review, f.collect); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE runtime_upgrade_native_public_startups SET machine_id=repeat('a',32)`,
		`DELETE FROM runtime_upgrade_native_public_startups`,
		`TRUNCATE runtime_upgrade_native_public_startups`,
		`INSERT INTO runtime_upgrade_native_public_startups SELECT public_session_id,slot_id,gateway_revision,public_revision,config_sha256,machine_id,boot_id,pid,start_ticks,pid_namespace,net_namespace,review,repeat('f',64),envelope,envelope_sha256,observed_at,recorded_at FROM runtime_upgrade_native_public_startups`,
		`INSERT INTO runtime_upgrade_native_public_startups SELECT public_session_id,slot_id,gateway_revision,public_revision,config_sha256,machine_id,boot_id,pid,'1',pid_namespace,net_namespace,review,review_sha256,envelope,envelope_sha256,observed_at,recorded_at FROM runtime_upgrade_native_public_startups`,
		`INSERT INTO runtime_upgrade_native_public_startups SELECT public_session_id,slot_id,gateway_revision,public_revision,config_sha256,machine_id,boot_id,pid,start_ticks,pid_namespace,net_namespace,review,review_sha256,convert_to('{}','UTF8'),encode(sha256(convert_to('{}','UTF8')),'hex'),observed_at,recorded_at FROM runtime_upgrade_native_public_startups`,
	} {
		_, err := f.pool.Exec(t.Context(), query)
		var rejected *pgconn.PgError
		if !errors.As(err, &rejected) || rejected.Code != "23514" {
			t.Fatal("expected evidence/immutability check, not duplicate-key failure", query, err)
		}
	}
	if f.count(t) != 1 {
		t.Fatal("immutable history lost")
	}
}
