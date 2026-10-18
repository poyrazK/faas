package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func cutoverDrainFixture(t *testing.T) (*PostgresStore, *state.PgStore, Cutover, state.Instance, state.ManagedPostgresDrain) {
	t.Helper()
	s, ps, c, node := admissionCutoverFixture(t)
	ctx := t.Context()
	d, err := ps.CreateDeployment(ctx, state.Deployment{AppID: c.AppID, Scope: "default", Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := ps.CreateInstance(ctx, c.AppID, d.ID, "running", 256, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	app, err := ps.AppByID(ctx, c.AppID)
	if err != nil {
		t.Fatal(err)
	}
	// Legacy fixtures intentionally leave apps unclaimed. Drain authority
	// requires a concrete durable scheduler owner, including single-box nodes.
	if _, err := s.pool.Exec(ctx, `UPDATE apps SET node_id=$2 WHERE id=$1`, c.AppID, node.ID); err != nil {
		t.Fatal(err)
	}
	app.NodeID = node.ID
	if _, err := ps.ReserveManagedPostgresDrain(ctx, c.ID, ins.ID, app.NodeID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("unfenced stop accepted", err)
	}
	if _, err := s.FenceCutoverAdmission(ctx, c.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	drain, err := ps.ReserveManagedPostgresDrain(ctx, c.ID, ins.ID, app.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	return s, ps, c, ins, drain
}

// Fabricated host observations test storage only, not native physical cleanup.
func cutoverDrainProof(f state.ManagedPostgresDrainFrame) state.ManagedPostgresDrainProof {
	return state.ManagedPostgresDrainProof{DrainID: f.ID, NativeGeneration: uuid.NewString(), KernelBootID: uuid.NewString(), ProcessesExited: true, ResourcesRemoved: true}
}

func TestPostgresCutoverDrainReceiptAndTerminalStateAreAtomic(t *testing.T) {
	s, ps, c, ins, drain := cutoverDrainFixture(t)
	f := drain.Frame
	if _, err := s.pool.Exec(t.Context(), `INSERT INTO container_host_port_leases(node_id,instance_id,listener_name,protocol,guest_port,host_port) VALUES($1,$2,'http','tcp',5432,30000)`, f.NodeID, f.InstanceID); err != nil {
		t.Fatal(err)
	}
	again, err := ps.ReserveManagedPostgresDrain(t.Context(), c.ID, ins.ID, f.OwnerNodeID)
	if err != nil || !again.Frame.Equal(f) {
		t.Fatal("reservation changed frame", err)
	}
	p := cutoverDrainProof(f)
	bad := p
	bad.ResourcesRemoved = false
	if err := ps.CompleteManagedPostgresDrain(t.Context(), f, bad); err == nil {
		t.Fatal("incomplete cleanup accepted")
	}
	if err := ps.CompleteManagedPostgresDrain(t.Context(), f, p); err != nil {
		t.Fatal(err)
	}
	stored, err := state.NewPgStore(s.pool).ManagedPostgresDrain(t.Context(), f.ID)
	current, loadErr := ps.InstanceByID(t.Context(), ins.ID)
	var terminal bool
	if err := s.pool.QueryRow(t.Context(), `SELECT terminal_at IS NOT NULL FROM instances WHERE id=$1`, ins.ID).Scan(&terminal); err != nil {
		t.Fatal(err)
	}
	if err != nil || loadErr != nil || stored.Proof == nil || *stored.Proof != p || stored.RetiredAt == nil || current.State != "stopped" || !terminal {
		t.Fatal("receipt and lifecycle diverged", stored, current, err, loadErr)
	}
	if err := ps.CompleteManagedPostgresDrain(t.Context(), f, p); err != nil {
		t.Fatal("lost commit response replay failed", err)
	}
	bad = p
	bad.NativeGeneration = uuid.NewString()
	if err := ps.CompleteManagedPostgresDrain(t.Context(), f, bad); !errors.Is(err, state.ErrConflict) {
		t.Fatal("receipt rewritten", err)
	}
	if _, err := s.pool.Exec(t.Context(), `UPDATE managed_postgres_cutover_drains SET kernel_boot_id=gen_random_uuid() WHERE id=$1`, f.ID); err == nil {
		t.Fatal("database allowed receipt rewrite")
	}
	if _, err := s.pool.Exec(t.Context(), `UPDATE managed_postgres_cutover_drains SET wake_id=gen_random_uuid() WHERE id=$1`, f.ID); err == nil {
		t.Fatal("database allowed frame rewrite")
	}
	rows, err := ps.ListPendingManagedPostgresDrains(t.Context(), f.OwnerNodeID, "", 1)
	if err != nil || len(rows) != 0 {
		t.Fatal("retired work rediscovered", err)
	}
	leases, err := ps.ListHostPortLeases(t.Context(), f.NodeID, f.InstanceID)
	if err != nil || len(leases) != 0 {
		t.Fatal("receipt commit retained host ports", err)
	}
	if _, err := s.pool.Exec(t.Context(), `DELETE FROM instances WHERE id=$1`, ins.ID); err != nil {
		t.Fatal(err)
	}
	retained, err := ps.ManagedPostgresDrain(t.Context(), f.ID)
	if err != nil || retained.Proof == nil || *retained.Proof != p {
		t.Fatal("terminal retention erased receipt", err)
	}
}

func TestPostgresCutoverDrainLateSQLFailureRollsBackReceiptAndPorts(t *testing.T) {
	s, ps, _, ins, drain := cutoverDrainFixture(t)
	f, p := drain.Frame, cutoverDrainProof(drain.Frame)
	if _, err := s.pool.Exec(t.Context(), `INSERT INTO container_host_port_leases(node_id,instance_id,listener_name,protocol,guest_port,host_port) VALUES($1,$2,'http','tcp',5432,30000)`, f.NodeID, f.InstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `CREATE FUNCTION reject_test_drain_stop() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected late write failure'; END $$; CREATE TRIGGER reject_test_drain_stop BEFORE UPDATE ON instances FOR EACH ROW EXECUTE FUNCTION reject_test_drain_stop()`); err != nil {
		t.Fatal(err)
	}
	if err := ps.CompleteManagedPostgresDrain(t.Context(), f, p); err == nil {
		t.Fatal("failed terminal write committed")
	}
	stored, err := ps.ManagedPostgresDrain(t.Context(), f.ID)
	current, loadErr := ps.InstanceByID(t.Context(), ins.ID)
	leases, leaseErr := ps.ListHostPortLeases(t.Context(), f.NodeID, f.InstanceID)
	if err != nil || loadErr != nil || leaseErr != nil || stored.Proof != nil || current.State != "running" || len(leases) != 1 {
		t.Fatal("partial commit released resources", stored, current, leases, err, loadErr, leaseErr)
	}
	if _, err := s.pool.Exec(t.Context(), `DROP FUNCTION reject_test_drain_stop() CASCADE`); err != nil {
		t.Fatal(err)
	}
	if err := ps.CompleteManagedPostgresDrain(t.Context(), f, p); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresCutoverDrainFailedNativeAndLostReplyStayPending(t *testing.T) {
	_, ps, _, ins, drain := cutoverDrainFixture(t)
	f, p := drain.Frame, cutoverDrainProof(drain.Frame)
	cause := errors.New("cleanup incomplete")
	if _, err := ps.WithManagedPostgresDrainAuthority(t.Context(), f, func(context.Context) (state.ManagedPostgresDrainProof, error) { return p, cause }); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	// Native retirement succeeds, but its reply is lost before schedd commits.
	if _, err := ps.WithManagedPostgresDrainAuthority(t.Context(), f, func(context.Context) (state.ManagedPostgresDrainProof, error) { return p, nil }); err != nil {
		t.Fatal(err)
	}
	pending, err := ps.ManagedPostgresDrain(t.Context(), f.ID)
	current, loadErr := ps.InstanceByID(t.Context(), ins.ID)
	if err != nil || loadErr != nil || pending.Proof != nil || current.State != "running" {
		t.Fatal("RPC success released durable state", pending, current, err, loadErr)
	}
	// A restarted scheduler redelivers the original member and commits evidence.
	if err := ps.CompleteManagedPostgresDrain(t.Context(), pending.Frame, p); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresCutoverDrainStaleFramesNeverReachNativeStop(t *testing.T) {
	for _, change := range []string{"wake", "node", "owner_revision", "owner_aba", "deployment", "migrating", "cancelled"} {
		t.Run(change, func(t *testing.T) {
			s, ps, c, ins, drain := cutoverDrainFixture(t)
			f, p := drain.Frame, cutoverDrainProof(drain.Frame)
			var err error
			switch change {
			case "wake":
				_, err = s.pool.Exec(t.Context(), `UPDATE instances SET wake_id=gen_random_uuid() WHERE id=$1`, ins.ID)
			case "node":
				other, loadErr := ps.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				_, err = s.pool.Exec(t.Context(), `UPDATE instances SET node_id=$2 WHERE id=$1`, ins.ID, other.ID)
			case "owner_revision":
				_, err = s.pool.Exec(t.Context(), `UPDATE apps SET max_concurrency=CASE WHEN max_concurrency=1 THEN 2 ELSE 1 END WHERE id=$1`, c.AppID)
			case "owner_aba":
				other, loadErr := ps.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				if _, err = s.pool.Exec(t.Context(), `UPDATE apps SET node_id=$2 WHERE id=$1`, c.AppID, other.ID); err == nil {
					_, err = s.pool.Exec(t.Context(), `UPDATE apps SET node_id=$2 WHERE id=$1`, c.AppID, f.OwnerNodeID)
				}
			case "deployment":
				err = execLegacyInstanceShape(t, s, `UPDATE instances SET deployment_id=NULL WHERE id=$1`, ins.ID)
			case "migrating":
				_, err = s.pool.Exec(t.Context(), `UPDATE instances SET state='migrating' WHERE id=$1`, ins.ID)
			case "cancelled":
				_, err = s.CancelCutover(t.Context(), c.AccountID, c.ID, time.Now())
				if err == nil {
					for _, member := range c.Credentials {
						claim, claimErr := s.ClaimCutover(t.Context(), c.AccountID, c.ID, uuid.NewString(), time.Now(), time.Now().Add(time.Minute))
						if claimErr != nil {
							t.Fatal(claimErr)
						}
						if err = s.RevokeCutoverCredential(t.Context(), claim, member, time.Now()); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			called := false
			if _, err := ps.WithManagedPostgresDrainAuthority(t.Context(), f, func(context.Context) (state.ManagedPostgresDrainProof, error) { called = true; return p, nil }); err == nil || called {
				t.Fatal("stale authority reached VM", err, called)
			}
			if err := ps.CompleteManagedPostgresDrain(t.Context(), f, p); err == nil {
				t.Fatal("stale receipt committed")
			}
		})
	}
}

func TestPostgresCutoverDrainLocksCancellationAndIncarnationDuringStop(t *testing.T) {
	s, ps, c, ins, drain := cutoverDrainFixture(t)
	f, p := drain.Frame, cutoverDrainProof(drain.Frame)
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	done := make(chan error, 1)
	go func() {
		_, err := ps.WithManagedPostgresDrainAuthority(t.Context(), f, func(context.Context) (state.ManagedPostgresDrainProof, error) {
			close(entered)
			<-release
			return p, nil
		})
		done <- err
	}()
	<-entered
	for _, mutation := range []string{"cancel", "wake", "owner"} {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		var err error
		switch mutation {
		case "cancel":
			_, err = s.CancelCutover(ctx, c.AccountID, c.ID, time.Now())
		case "wake":
			_, err = s.pool.Exec(ctx, `UPDATE instances SET wake_id=gen_random_uuid() WHERE id=$1`, ins.ID)
		case "owner":
			_, err = s.pool.Exec(ctx, `UPDATE apps SET max_concurrency=CASE WHEN max_concurrency=1 THEN 2 ELSE 1 END WHERE id=$1`, c.AppID)
		}
		cancel()
		if err == nil {
			t.Fatal("mutation raced physical stop", mutation)
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := ps.CompleteManagedPostgresDrain(t.Context(), f, p); err != nil {
		t.Fatal(err)
	}
}
