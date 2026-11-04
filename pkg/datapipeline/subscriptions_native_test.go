package datapipeline

import (
	"net/url"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestNativePauseAndDeleteStopOlderStreamsWithoutSourceLogin(t *testing.T) {
	for _, action := range []string{"pause", "delete"} {
		t.Run(action, func(t *testing.T) {
			f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id integer PRIMARY KEY,value text);INSERT INTO public.orders VALUES(1,'before pause')`)
			ctx, original := t.Context(), f.pipeline
			f.control(t, "resync")
			target := f.engine.Target
			f.engine.Target = nil
			f.tick(t)
			f.engine.Target = target
			if _, err := f.admin.Exec(ctx, "ALTER ROLE "+ident(f.role)+" PASSWORD 'replacement'"); err != nil {
				t.Fatal(err)
			}
			f.control(t, action)
			f.tick(t)
			wanted := "paused"
			if action == "delete" {
				wanted = "deleting"
			}
			if f.pipeline.State != wanted {
				t.Fatal("unavailable source produced the wrong control result", f.pipeline.DataPipeline)
			}
			for until := time.Now().Add(5 * time.Second); ; {
				var stopped bool
				if err := f.target.QueryRow(ctx, `SELECT NOT subenabled AND NOT EXISTS(SELECT 1 FROM pg_stat_subscription st WHERE st.subid=s.oid AND st.pid IS NOT NULL)
 FROM pg_subscription s WHERE subname=$1 AND subdbid=(SELECT oid FROM pg_database WHERE datname=current_database())`, pipelineName(original)).Scan(&stopped); err != nil {
					t.Fatal(err)
				}
				if stopped {
					break
				}
				if time.Now().After(until) {
					t.Fatal("acknowledged control left an older stream running")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if _, err := f.source.Exec(ctx, `UPDATE public.orders SET value='after acknowledged pause' WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			var value string
			if err := f.target.QueryRow(ctx, `SELECT value FROM public.orders WHERE id=1`).Scan(&value); err != nil || value != "before pause" {
				t.Fatal("a stopped stream continued applying changes", value, err)
			}
			u, _ := url.Parse(f.raw)
			u.User = url.UserPassword(f.role, "replacement")
			if _, err := f.service.RotateConnection(ctx, f.pipeline.AccountID, f.connection.ID, api.RotateDataConnectionRequest{ExpectedRevision: f.connection.Revision, ConnectionURL: u.String()}); err != nil {
				t.Fatal(err)
			}
			if action == "pause" {
				f.control(t, "resume")
				f.await(t, "live")
			} else {
				f.await(t, "deleted")
			}
			f.assertRetired(t, original)
		})
	}
}

func TestNativeCurrentSubscriptionRejectsForeignOwnershipBeforeChanges(t *testing.T) {
	for _, action := range []string{"reconcile", "pause", "delete"} {
		t.Run(action, func(t *testing.T) {
			f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id integer PRIMARY KEY,value text);INSERT INTO public.orders VALUES(1,'retained')`)
			ctx, name := t.Context(), pipelineName(f.pipeline)
			if _, err := f.target.Exec(ctx, "COMMENT ON SUBSCRIPTION "+ident(name)+" IS 'belongs to another owner'"); err != nil {
				t.Fatal(err)
			}
			if action != "reconcile" {
				f.control(t, action)
			}
			f.tick(t)
			if f.pipeline.Reason != "destination_subscription_conflict" || f.pipeline.State == "deleted" {
				t.Fatal("foreign subscription was adopted", f.pipeline.DataPipeline)
			}
			var preserved bool
			if err := f.target.QueryRow(ctx, `SELECT subenabled AND obj_description(s.oid,'pg_subscription')='belongs to another owner'
 AND NOT (SELECT retiring FROM gregale_pipeline.mirrors WHERE pipeline_id=$2)
 FROM pg_subscription s WHERE subname=$1 AND subdbid=(SELECT oid FROM pg_database WHERE datname=current_database())`, name, f.pipeline.ID).Scan(&preserved); err != nil || !preserved {
				t.Fatal("ownership rejection changed the subscription or receipt", preserved, err)
			}
		})
	}
}

func TestNativeLargeVerificationKeyFinishesInconclusive(t *testing.T) {
	f := newNativeRecoveryFixture(t, largeVerificationKeySQL("orders", 32))
	f.control(t, "verify")
	for until := time.Now().Add(20 * time.Second); ; {
		f.tick(t)
		v := f.pipeline.VerificationProgress
		if f.pipeline.Verification == "inconclusive" && !f.pipeline.VerifyRequested && v != nil && v.State == "limited" && v.Phase == "stopped" && v.Reason == "verification_key_limit" {
			break
		}
		if time.Now().After(until) || f.pipeline.State != "live" {
			t.Fatal("large key did not produce a terminal limit result", f.pipeline.State, f.pipeline.Reason)
		}
		time.Sleep(25 * time.Millisecond)
	}
	assertVerificationHelpersRetired(t, f.pipeline, f.target, relation{Name: "orders"})
	f.tick(t)
	if f.pipeline.Verification != "inconclusive" || f.pipeline.VerifyRequested {
		t.Fatal("limited verification retried automatically")
	}
}
