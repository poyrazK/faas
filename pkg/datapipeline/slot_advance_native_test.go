package datapipeline

import (
	"testing"
	"time"
)

func TestNativeAdvancedSlotRequiresFreshProof(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		missed, alreadyApplying bool
	}{
		{"unchanged rows", false, false},
		{"missed update", true, false},
		{"missed update with new sender applying", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			f.control(t, "pause")
			f.await(t, "paused")
			for until := time.Now().Add(5 * time.Second); ; {
				var active bool
				if err := f.source.QueryRow(ctx, `SELECT active FROM pg_replication_slots WHERE slot_name=$1`, pipelineName(f.pipeline)).Scan(&active); err != nil {
					t.Fatal(err)
				}
				if !active {
					break
				}
				if time.Now().After(until) {
					t.Fatal("paused source slot did not stop")
				}
				time.Sleep(20 * time.Millisecond)
			}
			before, err := loadSourceSlot(ctx, f.pipeline, f.target)
			if err != nil || before == nil {
				t.Fatal("stream witness unavailable", before, err)
			}
			if tc.missed {
				if _, err = f.source.Exec(ctx, `UPDATE public.orders SET value='skipped by slot advance' WHERE id=1`); err != nil {
					t.Fatal(err)
				}
			} else if _, err = f.source.Exec(ctx, `SELECT pg_logical_emit_message(false,'continuity_test','advance without changing selected rows')`); err != nil {
				t.Fatal(err)
			}
			if _, err = f.source.Exec(ctx, `SELECT pg_replication_slot_advance($1,pg_current_wal_lsn())`, pipelineName(f.pipeline)); err != nil {
				t.Fatal(err)
			}
			var overlappingAdvance bool
			if err = f.source.QueryRow(ctx, `SELECT restart_lsn<=$2::pg_lsn AND confirmed_flush_lsn>$2::pg_lsn FROM pg_replication_slots WHERE slot_name=$1`, pipelineName(f.pipeline), before.Confirmed).Scan(&overlappingAdvance); err != nil || !overlappingAdvance {
				t.Fatal("fixture did not retain overlapping WAL across slot advancement", overlappingAdvance, err)
			}
			f.control(t, "resume")
			if tc.alreadyApplying {
				if _, err = f.target.Exec(ctx, "ALTER SUBSCRIPTION "+ident(pipelineName(f.pipeline))+" ENABLE"); err != nil {
					t.Fatal(err)
				}
				if _, err = f.source.Exec(ctx, `UPDATE public.orders SET value='new stream works' WHERE id=2`); err != nil {
					t.Fatal(err)
				}
				for until := time.Now().Add(5 * time.Second); ; {
					var value string
					if err = f.target.QueryRow(ctx, `SELECT value FROM public.orders WHERE id=2`).Scan(&value); err == nil && value == "new stream works" {
						break
					}
					if time.Now().After(until) {
						t.Fatal("advanced stream did not apply subsequent changes", err)
					}
					time.Sleep(25 * time.Millisecond)
				}
			}
			f.tick(t)
			if tc.missed {
				if nativeReadAs(t, f, role, "alice") != 0 {
					t.Fatal("advanced slot left stale rows readable")
				}
				awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.State == "resync_required" })
				if f.pipeline.Reason != "replication_continuity_lost" {
					t.Fatal("slot advancement did not require explicit recovery", f.pipeline.DataPipeline)
				}
				var retained string
				if err = f.target.QueryRow(ctx, `SELECT value FROM public.orders WHERE id=1`).Scan(&retained); err != nil || retained != "before" {
					t.Fatal("continuity recovery replaced retained rows", retained, err)
				}
				f.control(t, "resync")
				f.await(t, "live")
				if err = f.target.QueryRow(ctx, `SELECT value FROM public.orders WHERE id=1`).Scan(&retained); err != nil || retained != "skipped by slot advance" {
					t.Fatal("explicit resync did not restore the skipped update", retained, err)
				}
				return
			}
			awaitNativeWorkflow(t, f, func(p Pipeline) bool {
				return p.State == "live" && p.Verification == "passed" && p.VerificationProgress.ID != oldProof
			})
			after, err := loadSourceSlot(ctx, f.pipeline, f.target)
			if err != nil || after == nil || after.Pending || after.Epoch == before.Epoch {
				t.Fatal("unchanged rows did not receive a fresh qualified epoch", after, err)
			}
			var preserved bool
			if err = f.target.QueryRow(ctx, `SELECT pg_relation_filenode('public.orders')=$1 AND (SELECT oid FROM pg_subscription WHERE subname=$2)=$3`, heap, pipelineName(f.pipeline), sub).Scan(&preserved); err != nil || !preserved || nativeReadAs(t, f, role, "alice") != 2 {
				t.Fatal("qualification lost table/subscription identity or reads", preserved, err)
			}
		})
	}
}
