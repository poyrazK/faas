package mailoutbox

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

func exerciseDeploymentFailedHandoff(t *testing.T, f *fixture, daemon *internalmail.Service, smtp *fixtureSMTP, gate, dropped *atomic.Bool) int {
	t.Helper()
	if err := daemon.Store.(*internalmail.PGStore).DeploymentFailedOutboxReady(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.env[internalmail.DeploymentFailedOutboxEnableEnv] = "1"
	if err := f.service.ConfigureDeploymentFailed(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	_, _, _, first := queueDeployment(t, f)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(first.ID.Bytes)); got.State != "queued" || len(got.SealedSecurityAlert) == 0 {
		t.Fatal("remote pause consumed failure notice")
	}
	gate.Store(true)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", first.ID); err != nil {
			t.Fatal(err)
		}
		next, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
		if err != nil {
			t.Fatal(err)
		}
		if err := next.ConfigureDeploymentFailed(t.Context(), func(k string) string { return f.env[k] }); err != nil {
			t.Fatal(err)
		}
		f.service = next
		if err := next.CaptureDeploymentFailures(t.Context()); err != nil {
			t.Fatal(err)
		}
		if attempt == 0 {
			dropped.Store(false)
		}
		if err := next.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		if attempt == 0 && row(t, f, uuidString(first.ID.Bytes)).State != "queued" {
			t.Fatal("test did not lose acknowledgement")
		}
	}
	if got := row(t, f, uuidString(first.ID.Bytes)); got.State != "admitted" {
		t.Fatal("failure notice lost admission")
	} else {
		assertMFAAlertErased(t, got)
	}
	before := smtp.accepted.Load()
	gate.Store(false)
	if err := daemon.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if smtp.accepted.Load() != before {
		t.Fatal("disabled template reached SMTP")
	}
	gate.Store(true)
	for range 2 {
		if err := daemon.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if smtp.accepted.Load() != before+1 {
		t.Fatal("lost acknowledgement duplicated SMTP custody")
	}
	admitted := 1
	for _, mode := range []string{"address_aba", "status_aba", "slug_aba", "owner_aba", "failure_aba", "success", "deployment_deleted", "deleted", "deleting", "erasure", "member_removed", "success_wait", "flag_wait"} {
		id, app, dep, intent := queueDeployment(t, f)
		if mode == "member_removed" {
			// Recreate the notice for an explicitly identified, authorized deployer.
			if _, err := f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", id); err != nil {
				t.Fatal(err)
			}
			id, app, dep = memberFailedDeployment(t, f, true)
			if err := f.service.CaptureDeploymentFailures(t.Context()); err != nil {
				t.Fatal(err)
			}
			var iid string
			if err := f.pool.QueryRow(t.Context(), "SELECT id FROM platform_mail_producer_outbox WHERE deployment_failure_id=$1", dep).Scan(&iid); err != nil {
				t.Fatal(err)
			}
			intent = row(t, f, iid)
		}
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(mode, err)
		}
		if row(t, f, uuidString(intent.ID.Bytes)).State != "admitted" {
			t.Fatal("notice not admitted", mode)
		}
		admitted++
		if mode == "success_wait" || mode == "flag_wait" {
			tx, err := f.pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer rollback(t.Context(), tx)
			if mode == "success_wait" {
				_, err = tx.Exec(t.Context(), "SELECT revision FROM platform_mail_deployment_fences WHERE app_id=$1 AND scope='default' FOR UPDATE", app)
			} else {
				_, err = tx.Exec(t.Context(), "SELECT id FROM accounts WHERE id=$1 FOR UPDATE", id)
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- daemon.DeliverOne(ctx) }()
			waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
			if mode == "success_wait" {
				if _, err := tx.Exec(ctx, "INSERT INTO deployments(id,app_id,status) VALUES($1,$2,'live')", uuid.NewString(), app); err != nil {
					t.Fatal(err)
				}
			} else {
				gate.Store(false)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if mode == "flag_wait" {
				var status string
				if err := f.pool.QueryRow(t.Context(), "SELECT r.status FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.id=r.message_id WHERE m.idempotency_key=$1", "deployment-failed:"+uuidString(intent.ID.Bytes)).Scan(&status); err != nil || status != "queued" {
					t.Fatal("withdrawn gate failed to hold notice", status, err)
				}
				gate.Store(true)
				mutateDeploymentFailure(t, f, "success", id, app, dep)
			}
		} else {
			mutateDeploymentFailure(t, f, mode, id, app, dep)
		}
		for range 2 {
			if err := daemon.DeliverOne(t.Context()); err != nil {
				t.Fatal(mode, err)
			}
		}
		if smtp.accepted.Load() != before+1 {
			t.Fatal("stale deployment failure reached SMTP", mode)
		}
		var status string
		if err := f.pool.QueryRow(t.Context(), "SELECT r.status FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.id=r.message_id WHERE m.idempotency_key=$1", "deployment-failed:"+uuidString(intent.ID.Bytes)).Scan(&status); err != nil || status != "cancelled" {
			t.Fatal(mode, status, err)
		}
	}
	f.env[internalmail.DeploymentFailedOutboxEnableEnv] = "0"
	return admitted
}
