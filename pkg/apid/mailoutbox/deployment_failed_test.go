package mailoutbox

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/mail"
)

const deploymentFailedMigration = "20261017155842001_platform_mail_deployment_failed_outbox.sql"

func deploymentFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	_, path := verificationIdentity(t)
	f.env[internalmail.DeploymentFailedOutboxEnableEnv], f.env[VerificationIdentityEnv] = "1", path
	configureDeploymentFixture(t, f)
	return f
}
func configureDeploymentFixture(t *testing.T, f *fixture) {
	t.Helper()
	if err := f.service.ConfigureDeploymentFailed(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	f.service.deploymentFailed.sender = f.sender
}
func failedDeployment(t *testing.T, f *fixture) (accountID, appID, deploymentID string) {
	t.Helper()
	accountID = account(t, f)
	appID = uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO apps(id,account_id,slug) VALUES($1,$2,'failed-app')", appID, accountID); err != nil {
		t.Fatal(err)
	}
	deploymentID = failAnotherDeployment(t, f, appID, "default")
	return
}
func failAnotherDeployment(t *testing.T, f *fixture, appID, scope string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO deployments(id,app_id,scope,status) VALUES($1,$2,$3,'pending')", id, appID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE deployments SET status='failed',error_code='build_failed',error='Authorization: Bearer abc.def.ghi',error_hint='Review the build configuration' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	return id
}
func queueDeployment(t *testing.T, f *fixture) (string, string, string, q.PlatformMailProducerOutbox) {
	t.Helper()
	id, app, dep := failedDeployment(t, f)
	if err := f.service.CaptureDeploymentFailures(t.Context()); err != nil {
		t.Fatal(err)
	}
	var intent string
	if err := f.pool.QueryRow(t.Context(), "SELECT id FROM platform_mail_producer_outbox WHERE deployment_failure_id=$1", dep).Scan(&intent); err != nil {
		t.Fatal(err)
	}
	return id, app, dep, row(t, f, intent)
}

func TestDeploymentFailedDisabledReadsOnlyOwnGate(t *testing.T) {
	var s *Service
	for _, value := range []string{"", "0", "true", " 1", "1 "} {
		if err := s.ConfigureDeploymentFailed(t.Context(), func(k string) string {
			if k != internalmail.DeploymentFailedOutboxEnableEnv {
				t.Fatal(k)
			}
			return value
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CaptureDeploymentFailures(t.Context()); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal(err)
	}
}

func TestPGDeploymentFailedFirstEnableAndRestartCapture(t *testing.T) {
	f := newFixture(t)
	_, _, old := failedDeployment(t, f)
	_, path := verificationIdentity(t)
	f.env[internalmail.DeploymentFailedOutboxEnableEnv], f.env[VerificationIdentityEnv] = "1", path
	configureDeploymentFixture(t, f)
	_, _, fresh := failedDeployment(t, f)
	var boundary time.Time
	if err := f.pool.QueryRow(t.Context(), "SELECT started_at FROM platform_mail_deployment_capture").Scan(&boundary); err != nil {
		t.Fatal(err)
	}
	next, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	f.service = next
	configureDeploymentFixture(t, f)
	if err := f.service.CaptureDeploymentFailures(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count, legacy int
	var after time.Time
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*),count(*) FILTER(WHERE deployment_failure_id=$1::uuid) FROM platform_mail_producer_outbox", old).Scan(&count, &legacy); err != nil || count != 1 || legacy != 0 {
		t.Fatal(count, legacy, err)
	}
	if err := f.pool.QueryRow(t.Context(), "SELECT started_at FROM platform_mail_deployment_capture").Scan(&after); err != nil || !after.Equal(boundary) {
		t.Fatal("restart moved activation boundary", err)
	}
	var got string
	if err := f.pool.QueryRow(t.Context(), "SELECT deployment_failure_id FROM platform_mail_producer_outbox").Scan(&got); err != nil || got != fresh {
		t.Fatal(got, err)
	}
}

func memberFailedDeployment(t *testing.T, f *fixture, authorized bool) (id, app, dep string) {
	t.Helper()
	id, owner, org := account(t, f), account(t, f), uuid.NewString()
	app, dep = uuid.NewString(), uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO apps(id,account_id,org_id,slug) VALUES($1,$2,$3,'failed-app')", app, owner, org); err != nil {
		t.Fatal(err)
	}
	if authorized {
		if _, err := f.pool.Exec(t.Context(), "INSERT INTO org_memberships(org_id,account_id,role) VALUES($1,$2,'developer')", org, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO deployments(id,app_id,status,deployed_by_user_id) VALUES($1,$2,'pending',$3)", dep, app, id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE deployments SET status='failed' WHERE id=$1", dep); err != nil {
		t.Fatal(err)
	}
	return
}

func TestPGDeploymentFailedRecipientAuthorizationAndRecoveryBoundary(t *testing.T) {
	for _, authorized := range []bool{false, true} {
		t.Run(map[bool]string{false: "unrelated_actor", true: "active_deployer"}[authorized], func(t *testing.T) {
			f := deploymentFixture(t)
			id, app, dep := memberFailedDeployment(t, f, authorized)
			if err := f.service.CaptureDeploymentFailures(t.Context()); err != nil {
				t.Fatal(err)
			}
			var disposition string
			var iid *string
			if err := f.pool.QueryRow(t.Context(), "SELECT disposition,intent_id::text FROM platform_mail_deployment_observations WHERE deployment_id=$1", dep).Scan(&disposition, &iid); err != nil {
				t.Fatal(err)
			}
			if !authorized {
				if disposition != "unavailable" || iid != nil {
					t.Fatal("unauthorized actor received mail", disposition, iid)
				}
				return
			}
			intent := row(t, f, *iid)
			if intent.AccountID != pgID(id) {
				t.Fatal("deployer not selected")
			}
			mutateDeploymentFailure(t, f, "member_removed", id, app, dep)
			if err := f.service.DeliverOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			if row(t, f, *iid).State != "cancelled" || len(f.sender.calls) != 0 {
				t.Fatal("removed member received notice")
			}
		})
	}
	f := deploymentFixture(t)
	_, _, old := failedDeployment(t, f)
	recoveryID := uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO internal_mail_recovery_state(namespace,recovery_id,held) VALUES('platform',$1,true)", recoveryID); err != nil {
		t.Fatal(err)
	}
	if err := f.service.CaptureDeploymentFailures(t.Context()); !errors.Is(err, internalmail.ErrRecoveryHeld) {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recovery_state SET held=false"); err != nil {
		t.Fatal(err)
	}
	if err := f.service.CaptureDeploymentFailures(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, _, fresh := failedDeployment(t, f)
	if err := f.service.CaptureDeploymentFailures(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count, oldCount int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*),count(*) FILTER(WHERE deployment_failure_id=$1::uuid) FROM platform_mail_producer_outbox", old).Scan(&count, &oldCount); err != nil || count != 1 || oldCount != 0 {
		t.Fatal("restored source history was replayed", count, oldCount, err)
	}
	var got string
	if err := f.pool.QueryRow(t.Context(), "SELECT deployment_failure_id FROM platform_mail_producer_outbox").Scan(&got); err != nil || got != fresh {
		t.Fatal(got, err)
	}
}

func TestPGDeploymentFailedCaptureRollbackAndConcurrentReplay(t *testing.T) {
	for _, mode := range []string{"intent", "receipt", "quota", "recovery"} {
		t.Run(mode, func(t *testing.T) {
			f := deploymentFixture(t)
			_, app, dep := failedDeployment(t, f)
			switch mode {
			case "intent":
				if _, err := f.pool.Exec(t.Context(), "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT reject_failure_intent CHECK(template<>'deployment_failed')"); err != nil {
					t.Fatal(err)
				}
			case "receipt":
				if _, err := f.pool.Exec(t.Context(), "ALTER TABLE platform_mail_deployment_observations ADD CONSTRAINT reject_failure_receipt CHECK(false)"); err != nil {
					t.Fatal(err)
				}
			case "recovery":
				if _, err := f.pool.Exec(t.Context(), "INSERT INTO internal_mail_recovery_state(namespace,recovery_id,held) VALUES('platform','00000000-0000-0000-0000-000000000000',true)"); err != nil {
					t.Fatal(err)
				}
			case "quota":
				seed := schedule(t, f, account(t, f))
				if _, err := f.pool.Exec(t.Context(), `INSERT INTO platform_mail_producer_outbox(id,account_id,namespace,producer_id,from_address,recipient_address,template,template_version,deletion_requested_at,restore_until,expires_at,recovery_id)
SELECT gen_random_uuid(),o.account_id,o.namespace,o.producer_id,o.from_address,o.recipient_address,o.template,o.template_version,o.deletion_requested_at-i*interval '1 second',o.restore_until,o.expires_at,o.recovery_id FROM platform_mail_producer_outbox o CROSS JOIN generate_series(1,$2) i WHERE o.id=$1`, seed.ID, api.InternalMailProducerOutboxMaxPending-1); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.service.CaptureDeploymentFailures(t.Context()); err == nil {
				t.Fatal("capture failure accepted")
			}
			var status string
			var cooldown *time.Time
			var observations int
			if err := f.pool.QueryRow(t.Context(), "SELECT d.status,p.last_deploy_failed_email_at,(SELECT count(*) FROM platform_mail_deployment_observations) FROM deployments d JOIN apps p ON p.id=d.app_id WHERE d.id=$1", dep).Scan(&status, &cooldown, &observations); err != nil || status != "failed" || cooldown != nil || observations != 0 {
				t.Fatal("mail failure changed source or consumed cooldown", status, cooldown, observations, err)
			}
			switch mode {
			case "intent":
				_, err := f.pool.Exec(t.Context(), "ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT reject_failure_intent")
				if err != nil {
					t.Fatal(err)
				}
			case "receipt":
				_, err := f.pool.Exec(t.Context(), "ALTER TABLE platform_mail_deployment_observations DROP CONSTRAINT reject_failure_receipt")
				if err != nil {
					t.Fatal(err)
				}
			case "quota":
				_, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET state='cancelled',recipient_address='' WHERE template='account_deletion_pending'")
				if err != nil {
					t.Fatal(err)
				}
			case "recovery":
				_, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recovery_state SET held=false")
				if err != nil {
					t.Fatal(err)
				}
			}
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					if err := f.service.CaptureDeploymentFailures(t.Context()); err != nil {
						t.Error(err)
					}
				})
			}
			wg.Wait()
			var intents int
			if err := f.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM platform_mail_producer_outbox WHERE template='deployment_failed'),(SELECT count(*) FROM platform_mail_deployment_observations),last_deploy_failed_email_at FROM apps WHERE id=$1", app).Scan(&intents, &observations, &cooldown); err != nil || intents != 1 || observations != 1 || cooldown == nil {
				t.Fatal(intents, observations, cooldown, err)
			}
		})
	}
}

func TestPGDeploymentFailedCooldownSkipsPermanently(t *testing.T) {
	f := deploymentFixture(t)
	_, app, _, _ := queueDeployment(t, f)
	second := failAnotherDeployment(t, f, app, "default")
	if err := f.service.CaptureDeploymentFailures(t.Context()); err != nil {
		t.Fatal(err)
	}
	var disposition string
	if err := f.pool.QueryRow(t.Context(), "SELECT disposition FROM platform_mail_deployment_observations WHERE deployment_id=$1", second).Scan(&disposition); err != nil || disposition != "cooldown" {
		t.Fatal(disposition, err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE apps SET last_deploy_failed_email_at=clock_timestamp()-interval '2 hours' WHERE id=$1", app); err != nil {
		t.Fatal(err)
	}
	if err := f.service.CaptureDeploymentFailures(t.Context()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM platform_mail_producer_outbox").Scan(&n); err != nil || n != 1 {
		t.Fatal("cooldown skip resurrected", n, err)
	}
	_ = failAnotherDeployment(t, f, app, "default")
	if err := f.service.CaptureDeploymentFailures(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM platform_mail_producer_outbox").Scan(&n); err != nil || n != 2 {
		t.Fatal(n, err)
	}
}

func TestPGDeploymentFailedLegacyClaimSurvivesRollbackWindow(t *testing.T) {
	f := deploymentFixture(t)
	_, app, dep := failedDeployment(t, f)
	// Model a failure and legacy claim two hours ago. Production source stamps
	// are immutable; only this isolated fixture disables its guard to set time.
	if _, err := f.pool.Exec(t.Context(), "ALTER TABLE deployments DISABLE TRIGGER deployments_mail_failure_guard"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE deployments SET mail_failed_at=clock_timestamp()-interval '2 hours' WHERE id=$1", dep); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "ALTER TABLE deployments ENABLE TRIGGER deployments_mail_failure_guard"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_deployment_capture SET started_at=clock_timestamp()-interval '3 hours'"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE apps SET last_deploy_failed_email_at=(SELECT mail_failed_at+interval '1 second' FROM deployments WHERE id=$2) WHERE id=$1", app, dep); err != nil {
		t.Fatal(err)
	}
	if err := f.service.CaptureDeploymentFailures(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int
	var disposition string
	if err := f.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM platform_mail_producer_outbox),disposition FROM platform_mail_deployment_observations WHERE deployment_id=$1", dep).Scan(&count, &disposition); err != nil || count != 0 || disposition != "cooldown" {
		t.Fatal("rollback resent legacy notice", count, disposition, err)
	}
}

func TestPGDeploymentFailedCaptureLockAllowsDeploymentParentFK(t *testing.T) {
	f := deploymentFixture(t)
	_, app, _ := failedDeployment(t, f)
	writer, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(t.Context(), writer)
	if _, err := writer.Exec(t.Context(), "INSERT INTO deployments(id,app_id,status) VALUES($1,$2,'pending')", uuid.NewString(), app); err != nil {
		t.Fatal(err)
	}
	// The writer retains its FK key-share and scope fence. Capture must still
	// acquire the app cooldown lock before waiting on that separate fence.
	reader, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(t.Context(), reader)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := f.service.queries.LockDeploymentFailureApp(ctx, reader, pgID(app)); err != nil {
		t.Fatal("cooldown lock conflicts with ordinary deployment FK", err)
	}
}

func TestPGDeploymentFailedFrozenPrivateSnapshotAndRetry(t *testing.T) {
	f := deploymentFixture(t)
	_, _, dep, intent := queueDeployment(t, f)
	payload, err := deploymentFailedPayloadFor(f.service.deploymentFailed, intent)
	if err != nil || strings.Contains(payload.Failure.Error, "abc.def.ghi") || len(payload.Failure.RelevantLogs) != 0 || !payload.Failure.FailedAt.Equal(intent.DeploymentFailureAt.Time) || !intent.ExpiresAt.Time.Equal(payload.Failure.FailedAt.Add(24*time.Hour)) {
		t.Fatal(payload, err)
	}
	if strings.Contains(string(intent.SealedSecurityAlert), "Review the build") || strings.Contains(string(intent.SealedSecurityAlert), "abc.def.ghi") {
		t.Fatal("payload contains plaintext")
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE deployments SET error='changed diagnostic',error_hint='changed hint' WHERE id=$1", dep); err != nil {
		t.Fatal(err)
	}
	f.sender.send = func(context.Context, mail.Message, internalmail.MessageTiming) error { return mail.ErrTransient }
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.sender.send = nil
	if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", intent.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.sender.calls) != 2 || !reflect.DeepEqual(f.sender.calls[0], f.sender.calls[1]) || strings.Contains(f.sender.calls[0].message.TextBody, "changed diagnostic") || f.sender.calls[0].message.MessageID != "deployment-failed:"+uuidString(intent.ID.Bytes) {
		t.Fatal("retry rewrote notice")
	}
	assertMFAAlertErased(t, row(t, f, uuidString(intent.ID.Bytes)))
	for _, mutate := range []func(*q.PlatformMailProducerOutbox){
		func(r *q.PlatformMailProducerOutbox) { r.AccountID = pgID(uuid.NewString()) }, func(r *q.PlatformMailProducerOutbox) { r.RecipientAddress = "another@example.net" },
		func(r *q.PlatformMailProducerOutbox) { r.DeploymentFailureRevision.Int64++ }, func(r *q.PlatformMailProducerOutbox) { r.DeploymentFailureAppRevision.Int64++ },
		func(r *q.PlatformMailProducerOutbox) { r.DeploymentFailureAccountRevision.Int64++ }, func(r *q.PlatformMailProducerOutbox) { r.DeploymentFailureSuccessRevision.Int64++ },
		func(r *q.PlatformMailProducerOutbox) { r.DeploymentFailureScope.String = "preview" }, func(r *q.PlatformMailProducerOutbox) { r.ExpiresAt.Time = r.ExpiresAt.Time.Add(time.Second) },
		func(r *q.PlatformMailProducerOutbox) { r.RecoveryID = pgID(uuid.NewString()) }, func(r *q.PlatformMailProducerOutbox) { r.DeploymentFailureID = pgID(uuid.NewString()) },
	} {
		copy := intent
		mutate(&copy)
		if _, err := deploymentFailedPayloadFor(f.service.deploymentFailed, copy); err == nil {
			t.Fatal("payload metadata substitution accepted")
		}
	}
}

func TestPGDeploymentFailedDiagnosticExpansionCannotBlockCapture(t *testing.T) {
	f := deploymentFixture(t)
	_, _, dep := failedDeployment(t, f)
	diagnostic := strings.Repeat("\"", api.InternalMailDeploymentFailedDiagnosticMaxBytes)
	if _, err := f.pool.Exec(t.Context(), "UPDATE deployments SET error_code=$2,error=$2,error_hint=$2,error_why=$2,error_fix=$2 WHERE id=$1", dep, diagnostic); err != nil {
		t.Fatal(err)
	}
	if err := f.service.CaptureDeploymentFailures(t.Context()); err != nil {
		t.Fatal("bounded JSON escaping blocked capture", err)
	}
	var iid string
	if err := f.pool.QueryRow(t.Context(), "SELECT id FROM platform_mail_producer_outbox WHERE deployment_failure_id=$1", dep).Scan(&iid); err != nil {
		t.Fatal(err)
	}
	payload, err := deploymentFailedPayloadFor(f.service.deploymentFailed, row(t, f, iid))
	if err != nil {
		t.Fatal(err)
	}
	if payload.Failure.ErrorCode != diagnostic || payload.Failure.ErrorWhy != "" {
		t.Fatal("summary priority/bounds lost")
	}
}

func mutateDeploymentFailure(t *testing.T, f *fixture, mode, id, app, dep string) {
	t.Helper()
	var err error
	switch mode {
	case "address_aba":
		_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET email='changed@example.net' WHERE id=$1", id)
		if err == nil {
			_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET email='one@example.net' WHERE id=$1", id)
		}
	case "status_aba":
		_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET status='suspended' WHERE id=$1", id)
		if err == nil {
			_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET status='active' WHERE id=$1", id)
		}
	case "slug_aba":
		_, err = f.pool.Exec(t.Context(), "UPDATE apps SET slug='changed-app' WHERE id=$1", app)
		if err == nil {
			_, err = f.pool.Exec(t.Context(), "UPDATE apps SET slug='failed-app' WHERE id=$1", app)
		}
	case "owner_aba":
		other := account(t, f)
		_, err = f.pool.Exec(t.Context(), "UPDATE apps SET account_id=$2 WHERE id=$1", app, other)
		if err == nil {
			_, err = f.pool.Exec(t.Context(), "UPDATE apps SET account_id=$2 WHERE id=$1", app, id)
		}
	case "failure_aba":
		_, err = f.pool.Exec(t.Context(), "UPDATE deployments SET status='pending' WHERE id=$1", dep)
		if err == nil {
			_, err = f.pool.Exec(t.Context(), "UPDATE deployments SET status='failed' WHERE id=$1", dep)
		}
	case "success", "other_scope_success":
		scope := "default"
		if mode == "other_scope_success" {
			scope = "preview"
		}
		_, err = f.pool.Exec(t.Context(), "INSERT INTO deployments(id,app_id,scope,status) VALUES($1,$2,$3,'live')", uuid.NewString(), app, scope)
	case "deleted":
		_, err = f.pool.Exec(t.Context(), "UPDATE apps SET status='deleted' WHERE id=$1", app)
	case "deployment_deleted":
		_, err = f.pool.Exec(t.Context(), "UPDATE deployments SET deleted_at=clock_timestamp() WHERE id=$1", dep)
	case "deleting":
		_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET deletion_requested_at=clock_timestamp() WHERE id=$1", id)
	case "erasure":
		_, err = f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", id)
	case "unrelated":
		_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET plan='pro',mfa_revision=mfa_revision+1,password_reset_revision=password_reset_revision+1 WHERE id=$1", id)
	case "suppressed":
		_, err = f.pool.Exec(t.Context(), "INSERT INTO mail_suppressions(email) VALUES('one@example.net')")
	case "member_removed":
		_, err = f.pool.Exec(t.Context(), "UPDATE org_memberships SET removed_at=clock_timestamp() WHERE account_id=$1", id)
	}
	if err != nil {
		t.Fatal(mode, err)
	}
}

func TestPGDeploymentFailedStaleSuppressionAndScopeIsolation(t *testing.T) {
	for _, mode := range []string{"address_aba", "status_aba", "slug_aba", "owner_aba", "failure_aba", "success", "other_scope_success", "deleted", "deployment_deleted", "deleting", "erasure", "unrelated", "suppressed", "off"} {
		t.Run(mode, func(t *testing.T) {
			f := deploymentFixture(t)
			id, app, dep, intent := queueDeployment(t, f)
			if mode == "off" {
				f.env[internalmail.DeploymentFailedOutboxEnableEnv] = "0"
			} else {
				mutateDeploymentFailure(t, f, mode, id, app, dep)
			}
			if err := f.service.DeliverOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			if mode == "unrelated" || mode == "other_scope_success" {
				if len(f.sender.calls) != 1 {
					t.Fatal("unrelated change cancelled failure notice")
				}
				return
			}
			if len(f.sender.calls) != 0 {
				t.Fatal("stale failure notice sent")
			}
			if mode == "erasure" {
				var n int
				if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM platform_mail_deployment_observations").Scan(&n); err != nil || n != 0 {
					t.Fatal(n, err)
				}
				return
			}
			want := "cancelled"
			if mode == "suppressed" {
				want = "suppressed"
			}
			if mode == "off" {
				want = "queued"
			}
			got := row(t, f, uuidString(intent.ID.Bytes))
			if got.State != want {
				t.Fatal(got.State, want)
			}
			if want != "queued" {
				assertMFAAlertErased(t, got)
			}
		})
	}
}

func TestPGDeploymentFailedWithdrawalDuringSave(t *testing.T) {
	f := deploymentFixture(t)
	_, app, _ := failedDeployment(t, f)
	var on atomic.Bool
	on.Store(true)
	f.service.deploymentFailed.enabled = on.Load
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(t.Context(), tx)
	if _, err := tx.Exec(t.Context(), "LOCK TABLE platform_mail_producer_outbox IN SHARE MODE"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.service.CaptureDeploymentFailures(ctx) }()
	waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
	on.Store(false)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal(err)
	}
	var n int
	var cooldown *time.Time
	if err := f.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM platform_mail_deployment_observations),last_deploy_failed_email_at FROM apps WHERE id=$1", app).Scan(&n, &cooldown); err != nil || n != 0 || cooldown != nil {
		t.Fatal("withdrawal consumed failure", n, cooldown, err)
	}
}

func TestPGDeploymentFailedReadinessAndUsedHistoryRollback(t *testing.T) {
	f := deploymentFixture(t)
	for _, trigger := range []struct{ table, name string }{{"accounts", "accounts_deployment_mail_revision_guard"}, {"apps", "apps_deployment_mail_revision_guard"}, {"deployments", "deployments_mail_failure_guard"}} {
		if _, err := f.pool.Exec(t.Context(), "ALTER TABLE "+trigger.table+" DISABLE TRIGGER "+trigger.name); err != nil {
			t.Fatal(err)
		}
		if err := f.service.ConfigureDeploymentFailed(t.Context(), func(k string) string { return f.env[k] }); err == nil {
			t.Fatal("disabled source fence accepted")
		}
		if _, err := f.pool.Exec(t.Context(), "ALTER TABLE "+trigger.table+" ENABLE TRIGGER "+trigger.name); err != nil {
			t.Fatal(err)
		}
	}
	up, down := migrationFileParts(t, deploymentFailedMigration)
	if _, err := f.pool.Exec(t.Context(), down); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ConfigureDeploymentFailed(t.Context(), func(k string) string { return f.env[k] }); err == nil {
		t.Fatal("old schema accepted")
	}
	if _, err := f.pool.Exec(t.Context(), up); err != nil {
		t.Fatal(err)
	}
	configureDeploymentFixture(t, f)
	id, app, dep, intent := queueDeployment(t, f)
	if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET deployment_mail_revision=1 WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE apps SET deployment_mail_revision=1 WHERE id=$1", app); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE deployments SET mail_failure_revision=1 WHERE id=$1", dep); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if row(t, f, uuidString(intent.ID.Bytes)).State != "cancelled" {
		t.Fatal("counter rewinds revived notice")
	}
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded history")
	}
}
