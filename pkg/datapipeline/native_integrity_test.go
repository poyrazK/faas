package datapipeline

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type unavailableWitnessSource struct{}

func (unavailableWitnessSource) QueryRow(ctx context.Context, _ string, _ ...any) pgx.Row {
	<-ctx.Done()
	return witnessSourceError{ctx.Err()}
}

type witnessSourceError struct{ err error }

func (r witnessSourceError) Scan(...any) error { return r.err }

func TestNativeStreamWitnessTimeoutCannotPreventPause(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id int PRIMARY KEY);INSERT INTO public.orders VALUES(1)`)
	ctx := t.Context()
	err := rememberSourceSlotBeforePause(ctx, f.pipeline, unavailableWitnessSource{}, f.target, func(context.Context) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("optional source bookkeeping was not bounded", err)
	}
	if err = setSubscriptionEnabled(ctx, f.pipeline, f.target, false); err != nil {
		t.Fatal("source timeout prevented native pause", err)
	}
	var enabled bool
	if err = f.target.QueryRow(ctx, `SELECT subenabled FROM pg_subscription WHERE subname=$1`, pipelineName(f.pipeline)).Scan(&enabled); err != nil || enabled {
		t.Fatal("native subscription was not disabled", enabled, err)
	}
}

func TestNativeStreamWitnessAndReadGateShareLeaseFence(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id int PRIMARY KEY,value text);INSERT INTO public.orders VALUES(1,'retained')`)
	ctx := t.Context()
	role := nativeReaderRole(t, f)
	nativePauseAndLoseMainSlot(t, f)
	before, err := loadSourceSlot(ctx, f.pipeline, f.target)
	if err != nil || before == nil {
		t.Fatal("stream witness was not initialized", before, err)
	}
	if _, err = f.source.Exec(ctx, `SELECT pg_create_logical_replication_slot($1,'pgoutput')`, pipelineName(f.pipeline)); err != nil {
		t.Fatal(err)
	}
	err = recordSourceSlot(ctx, f.pipeline, f.source, f.target, false, func(context.Context) error { return ErrConflict })
	if !errors.Is(err, ErrConflict) {
		t.Fatal("lost lease committed a new stream baseline", err)
	}
	after, err := loadSourceSlot(ctx, f.pipeline, f.target)
	if err != nil || !reflect.DeepEqual(before, after) || nativeReadAs(t, f, role, "alice") != 1 {
		t.Fatal("rejected fence partially committed witness or reads", after, err)
	}
	if err = recordSourceSlot(ctx, f.pipeline, f.source, f.target, false, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	after, err = loadSourceSlot(ctx, f.pipeline, f.target)
	if err != nil || after == nil || !after.Pending || after.Epoch == before.Epoch || nativeReadAs(t, f, role, "alice") != 0 {
		t.Fatal("replacement did not atomically save a gated epoch", after, err)
	}
	var change, phase string
	if err = f.target.QueryRow(ctx, `SELECT schema_change,schema_phase FROM gregale_pipeline.mirrors WHERE pipeline_id=$1`, f.pipeline.ID).Scan(&change, &phase); err != nil || change != sourceContinuityFence(f.pipeline) || change == sourceStorageFence(f.pipeline) || phase != "verifying" {
		t.Fatal("continuity gate can be adopted as an older storage check", change, phase, err)
	}
	err = allowMirrorReads(ctx, f.pipeline, f.target, true)
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23514" || pg.ConstraintName != "source_stream_read_gate" || nativeReadAs(t, f, role, "alice") != 0 {
		t.Fatal("an older read-opening transaction bypassed continuity qualification", err)
	}
}

func TestNativeStreamWitnessNeedsCoveredCheckpoint(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id int PRIMARY KEY);INSERT INTO public.orders VALUES(1)`)
	ctx := t.Context()
	w := &sourceSlotWitness{Peer: "previous-sender"}
	if err := f.source.QueryRow(ctx, `SELECT (pg_current_wal_lsn()+1048576)::text`).Scan(&w.Confirmed); err != nil {
		t.Fatal(err)
	}
	if _, continuous, err := sampleSourceSlot(ctx, f.pipeline, f.source, w); err != nil || continuous {
		t.Fatal("a new sender cannot cover a checkpoint beyond its confirmed WAL", continuous, err)
	}
}

func TestNativeFullSchemaProofRequalifiesReplacedSlot(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id int PRIMARY KEY,value text);INSERT INTO public.orders VALUES(1,'retained')`)
	ctx := t.Context()
	role := nativeReaderRole(t, f)
	ready := false
	f.engine.RefreshSchema = func(context.Context, Pipeline, SchemaChange) ([]SchemaRefresh, bool, error) {
		return nil, ready, nil
	}
	if _, err := f.source.Exec(ctx, `ALTER TABLE public.orders ADD COLUMN extra text;UPDATE public.orders SET extra='approved' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.Reason == "schema_change_pending" })
	approval := f.pipeline.SchemaChange.ID
	if err := f.store.ApproveSchema(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, approval, false); err != nil {
		t.Fatal(err)
	}
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.Reason == "schema_api_refresh_pending" })
	oldProof := f.pipeline.VerificationProgress.ID
	generation := f.pipeline.Generation
	nativePauseAndLoseMainSlot(t, f)
	if _, err := f.source.Exec(ctx, `SELECT pg_create_logical_replication_slot($1,'pgoutput')`, pipelineName(f.pipeline)); err != nil {
		t.Fatal(err)
	}
	f.control(t, "resume")
	awaitNativeWorkflow(t, f, func(p Pipeline) bool {
		return p.Reason == "schema_api_refresh_pending" && p.VerificationProgress.ID != oldProof
	})
	if f.pipeline.SchemaApproval != approval || f.pipeline.SchemaChange.ID != approval || f.pipeline.Generation != generation {
		t.Fatal("continuity check discarded the approved schema workflow", f.pipeline.DataPipeline)
	}
	var change string
	if err := f.target.QueryRow(ctx, `SELECT schema_change FROM gregale_pipeline.mirrors WHERE pipeline_id=$1`, f.pipeline.ID).Scan(&change); err != nil || change != approval {
		t.Fatal("full-schema continuity lost its original receipt", change, err)
	}
	err := allowMirrorReads(ctx, f.pipeline, f.target, true)
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23514" || pg.ConstraintName != "source_stream_read_gate" || nativeReadAs(t, f, role, "alice") != 0 {
		t.Fatal("an older schema worker could open an unfinished continuity proof", err)
	}
	ready = true
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.State == "live" && p.SchemaChange.State == "complete" })
	w, err := loadSourceSlot(ctx, f.pipeline, f.target)
	if err != nil || w == nil || w.Pending || nativeReadAs(t, f, role, "alice") != 1 || f.pipeline.Generation != generation {
		t.Fatal("full-schema proof failed to release its qualified read gate", w, err)
	}
}

func TestNativeReadyRelationLossRequiresResync(t *testing.T) {
	f := nativeTwoTableFixture(t)
	ctx := t.Context()
	role := nativeReaderRole(t, f)
	name := pipelineName(f.pipeline)
	if _, err := f.source.Exec(ctx, "ALTER PUBLICATION "+ident(name)+" DROP TABLE public.orders"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.target.Exec(ctx, "ALTER SUBSCRIPTION "+ident(name)+" REFRESH PUBLICATION WITH(copy_data=false)"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.source.Exec(ctx, "ALTER PUBLICATION "+ident(name)+" ADD TABLE ONLY public.orders;UPDATE public.orders SET value='missed CDC' WHERE id=1;UPDATE public.customers SET name='healthy CDC' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	for until := time.Now().Add(5 * time.Second); ; {
		var value string
		if err := f.target.QueryRow(ctx, `SELECT name FROM public.customers WHERE id=1`).Scan(&value); err == nil && value == "healthy CDC" {
			break
		}
		if time.Now().After(until) {
			t.Fatal("healthy stream did not advance")
		}
		time.Sleep(25 * time.Millisecond)
	}
	f.tick(t)
	if f.pipeline.State != "resync_required" || f.pipeline.Reason != "replication_state_lost" || nativeReadAs(t, f, role, "alice") != 0 {
		t.Fatal("missing ready tracking was treated as initial copy", f.pipeline.DataPipeline)
	}
	f.assertRetired(t, f.pipeline)
	var retained string
	if err := f.target.QueryRow(ctx, `SELECT value FROM public.orders WHERE id=1`).Scan(&retained); err != nil || retained != "before" {
		t.Fatal("recovery replaced imported rows without approval", retained, err)
	}
	f.control(t, "resync")
	f.await(t, "live")
	if err := f.target.QueryRow(ctx, `SELECT value FROM public.orders WHERE id=1`).Scan(&retained); err != nil || retained != "missed CDC" {
		t.Fatal("explicit recovery did not restore data", retained, err)
	}
}

func TestNativeReplacedSlotRequiresFreshProof(t *testing.T) {
	for _, missed := range []bool{false, true} {
		name := "unchanged rows"
		if missed {
			name = "missed rows with new stream already applying"
		}
		t.Run(name, func(t *testing.T) {
			f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id int PRIMARY KEY,value text);INSERT INTO public.orders VALUES(1,'before'),(2,'sentinel')`)
			ctx := t.Context()
			role := nativeReaderRole(t, f)
			f.control(t, "verify")
			awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.Verification == "passed" })
			oldProof := f.pipeline.VerificationProgress.ID
			var heap, sub uint32
			if err := f.target.QueryRow(ctx, `SELECT pg_relation_filenode('public.orders'),(SELECT oid FROM pg_subscription WHERE subname=$1)`, pipelineName(f.pipeline)).Scan(&heap, &sub); err != nil {
				t.Fatal(err)
			}
			nativePauseAndLoseMainSlot(t, f)
			if missed {
				if _, err := f.source.Exec(ctx, `UPDATE public.orders SET value='lost while paused' WHERE id=1`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.source.Exec(ctx, `SELECT pg_create_logical_replication_slot($1,'pgoutput')`, pipelineName(f.pipeline)); err != nil {
				t.Fatal(err)
			}
			f.control(t, "resume")
			if missed {
				// Native progress can advance beyond a gap before the worker polls.
				// A current origin/heartbeat cannot establish historical continuity.
				if _, err := f.target.Exec(ctx, "ALTER SUBSCRIPTION "+ident(pipelineName(f.pipeline))+" ENABLE"); err != nil {
					t.Fatal(err)
				}
				if _, err := f.source.Exec(ctx, `UPDATE public.orders SET value='new stream works' WHERE id=2`); err != nil {
					t.Fatal(err)
				}
				for until := time.Now().Add(5 * time.Second); ; {
					var value string
					if err := f.target.QueryRow(ctx, `SELECT value FROM public.orders WHERE id=2`).Scan(&value); err == nil && value == "new stream works" {
						break
					}
					if time.Now().After(until) {
						t.Fatal("replacement stream did not apply subsequent changes")
					}
					time.Sleep(25 * time.Millisecond)
				}
			}
			f.tick(t)
			if missed {
				if nativeReadAs(t, f, role, "alice") != 0 {
					t.Fatal("replacement slot left stale rows readable")
				}
				awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.State == "resync_required" })
				if f.pipeline.Reason != "replication_continuity_lost" {
					t.Fatal("gap did not require explicit recovery", f.pipeline.DataPipeline)
				}
				var retained string
				if err := f.target.QueryRow(ctx, `SELECT value FROM public.orders WHERE id=1`).Scan(&retained); err != nil || retained != "before" {
					t.Fatal("continuity recovery replaced data", retained, err)
				}
				f.control(t, "resync")
				f.await(t, "live")
				return
			}
			awaitNativeWorkflow(t, f, func(p Pipeline) bool {
				return p.State == "live" && p.Verification == "passed" && p.VerificationProgress.ID != oldProof
			})
			var preserved bool
			if err := f.target.QueryRow(ctx, `SELECT pg_relation_filenode('public.orders')=$1 AND (SELECT oid FROM pg_subscription WHERE subname=$2)=$3 AND (SELECT schema_phase='' AND NOT coalesce((source_slot->>'Pending')::boolean,false) FROM gregale_pipeline.mirrors WHERE pipeline_id=$4)`, heap, pipelineName(f.pipeline), sub, f.pipeline.ID).Scan(&preserved); err != nil || !preserved || nativeReadAs(t, f, role, "alice") != 2 {
				t.Fatal("successful qualification lost storage, identity or reads", preserved, err)
			}
		})
	}
}

func TestNativeRetainedSlotReconnectRecovers(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id int PRIMARY KEY,value text);INSERT INTO public.orders VALUES(1,'before')`)
	ctx := t.Context()
	var database string
	if err := f.source.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.Exec(ctx, "ALTER ROLE "+ident(f.role)+" NOSUPERUSER REPLICATION;GRANT CREATE ON DATABASE "+ident(database)+" TO "+ident(f.role)); err != nil {
		t.Fatal(err)
	}
	f.control(t, "pause")
	f.await(t, "paused")
	if _, err := f.source.Exec(ctx, `UPDATE public.orders SET value='retained WAL' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	f.control(t, "resume")
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.State == "live" })
	awaitNativeWorkflow(t, f, func(Pipeline) bool {
		var value string
		return f.target.QueryRow(ctx, `SELECT value FROM public.orders WHERE id=1`).Scan(&value) == nil && value == "retained WAL"
	})
	if f.pipeline.Verification != "unknown" && f.pipeline.Verification != "passed" {
		t.Fatal("retained slot failed to requalify its resumed sender", f.pipeline.Verification)
	}
}

func TestNativeReadyBoundarySurvivesLostObservationAndClosedReads(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id int PRIMARY KEY,value text);INSERT INTO public.orders VALUES(1,'retained')`)
	ctx := t.Context()
	if err := allowMirrorReads(ctx, f.pipeline, f.target, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.pool.Exec(ctx, `UPDATE data_pipelines SET state='copying',tables_ready=0 WHERE id=$1`, f.pipeline.ID); err != nil {
		t.Fatal(err)
	}
	name := pipelineName(f.pipeline)
	if _, err := f.source.Exec(ctx, "ALTER PUBLICATION "+ident(name)+" DROP TABLE public.orders"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.target.Exec(ctx, "ALTER SUBSCRIPTION "+ident(name)+" REFRESH PUBLICATION WITH(copy_data=false)"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.source.Exec(ctx, "ALTER PUBLICATION "+ident(name)+" ADD TABLE ONLY public.orders"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.target.Exec(ctx, "ALTER SUBSCRIPTION "+ident(name)+" REFRESH PUBLICATION WITH(copy_data=true)"); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	if f.pipeline.State != "resync_required" || f.pipeline.Reason != "replication_state_lost" {
		t.Fatal("lost catalog acknowledgement erased native readiness", f.pipeline.DataPipeline)
	}
}

func TestNativeLegacyStreamReceiptRequiresQualification(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id int PRIMARY KEY,value text);INSERT INTO public.orders VALUES(1,'retained')`)
	ctx := t.Context()
	f.control(t, "verify")
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.Verification == "passed" })
	old := f.pipeline.VerificationProgress.ID
	if _, err := f.target.Exec(ctx, `ALTER TABLE gregale_pipeline.mirrors DROP COLUMN source_slot`); err != nil {
		t.Fatal(err)
	}
	awaitNativeWorkflow(t, f, func(p Pipeline) bool {
		return p.State == "live" && p.Verification == "passed" && p.VerificationProgress.ID != old
	})
	var valid bool
	if err := f.target.QueryRow(ctx, `SELECT schema_phase='' AND readable AND source_slot->>'Epoch'<>'' AND NOT coalesce((source_slot->>'Pending')::boolean,false) FROM gregale_pipeline.mirrors WHERE pipeline_id=$1`, f.pipeline.ID).Scan(&valid); err != nil || !valid {
		t.Fatal("legacy stream was adopted without a new qualified receipt", valid, err)
	}
}

func TestNativeApprovedSchemaResumeWaitsForAPIRefresh(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id int PRIMARY KEY,value text);INSERT INTO public.orders VALUES(1,'retained')`)
	ctx := t.Context()
	ready := false
	f.engine.RefreshSchema = func(context.Context, Pipeline, SchemaChange) ([]SchemaRefresh, bool, error) {
		return nil, ready, nil
	}
	if _, err := f.source.Exec(ctx, `ALTER TABLE public.orders ADD COLUMN extra text`); err != nil {
		t.Fatal(err)
	}
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.Reason == "schema_change_pending" })
	if err := f.store.ApproveSchema(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, f.pipeline.SchemaChange.ID, false); err != nil {
		t.Fatal(err)
	}
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.Reason == "schema_api_refresh_pending" })
	f.control(t, "pause")
	f.await(t, "paused")
	f.control(t, "resume")
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.Reason == "schema_api_refresh_pending" })
	t.Logf("real resumed worker: state=%s reason=%s fresh=%t approved=%t", f.pipeline.State, f.pipeline.Reason, f.pipeline.ObservationFresh, f.pipeline.SchemaApproval == f.pipeline.SchemaChange.ID)
	ready = true
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.State == "live" && p.SchemaChange.State == "complete" })
	t.Log("same workflow completes live once its existing API refresh finishes")
}
