package mailoutbox

import (
	"context"
	"sync"
	"testing"
	"time"

	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// Catalog deparsing must never inspect another schema's relation. Its DDL can
// commit after the catalog scan's snapshot but before pg_get_constraintdef's
// relation lookup, even though the readiness predicate excludes that schema.
func TestPGMailReadinessIgnoresForeignSchemaChurn(t *testing.T) {
	f := newFixture(t)
	foreign := pgtest.Open(t)
	conn, err := f.pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(t.Context(), "SET enable_nestloop=off; SET enable_mergejoin=off; SET enable_indexscan=off; SET enable_bitmapscan=off"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	var wg sync.WaitGroup
	errors := make(chan error, 1)
	wg.Go(func() {
		for ctx.Err() == nil {
			_, err := foreign.Exec(ctx, `DROP TABLE IF EXISTS platform_mail_producer_outbox;
CREATE TABLE platform_mail_producer_outbox(value text CONSTRAINT platform_mail_producer_outbox_security_alert_check
CHECK(value IN ('mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning','subscription_ended')))`)
			if err != nil && ctx.Err() == nil {
				errors <- err
				return
			}
		}
	})
	defer func() { cancel(); wg.Wait() }()
	checks := []func(context.Context, q.DBTX) (bool, error){
		f.service.queries.MFADisabledAlertReady, f.service.queries.PasswordChangedAlertReady,
		f.service.queries.MFAEnabledAlertReady, f.service.queries.PaymentFailedReady,
		f.service.queries.AccountRestoredReady, f.service.queries.AccountSuspendedReady,
		f.service.queries.NonpaymentDeletionReady, f.service.queries.QuotaWarningReady, f.service.queries.SubscriptionEndedReady, f.service.queries.DeploymentFailedReady,
	}
	for range 100 {
		for i, check := range checks {
			if ready, err := check(ctx, conn); err != nil || !ready {
				t.Fatal("foreign DDL broke readiness", i, ready, err)
			}
		}
		select {
		case err := <-errors:
			t.Fatal(err)
		default:
		}
	}
}
