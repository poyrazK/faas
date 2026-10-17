//go:build !no_pg

package apidserver_test

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestDeploymentFailedOutboxOwnsSelectedSubscriberWithoutFallback(t *testing.T) {
	for _, mode := range []string{"off", "on", "intent_failure", "late_enable"} {
		t.Run(mode, func(t *testing.T) {
			e := apidserver.SetupPGHandler(t, api.PlanPro)
			s, store, pool := *e.S(), *e.Store(), *e.Pool()
			app, err := store.CreateApp(t.Context(), state.App{AccountID: e.Acct().ID, Slug: "failed-mail", Type: state.AppTypeApp})
			if err != nil {
				t.Fatal(err)
			}
			flags := paymentFlags(t, false)
			if mode == "on" || mode == "intent_failure" {
				flags[internalmail.DeploymentFailedOutboxEnableEnv] = "1"
			}
			if err := s.ConfigureMailOutbox(t.Context(), pool, func(k string) string { return flags[k] }); err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Status: state.DeployPending, Kind: state.DeploymentKindImage})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.SetDeploymentFailedEx(t.Context(), dep.ID, "build_failed", "build failed", "check configuration", "", "", nil); err != nil {
				t.Fatal(err)
			}
			recorder := &paymentInlineRecorder{}
			*s.Mailer() = recorder
			if mode == "late_enable" {
				flags[internalmail.DeploymentFailedOutboxEnableEnv] = "1"
			}
			if mode == "intent_failure" {
				if _, err := pool.Exec(t.Context(), "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT reject_failed_mail CHECK(template<>'deployment_failed')"); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := s.SendDeployFailedEmail(t.Context(), dep.ID); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "on" || mode == "intent_failure" {
				err := s.CaptureDeploymentFailureMail(t.Context())
				if (mode == "intent_failure") != (err != nil) {
					t.Fatal("incorrect capture failure", err)
				}
			}
			var intents int
			var status string
			var cooldown bool
			if err := pool.QueryRow(t.Context(), "SELECT d.status,p.last_deploy_failed_email_at IS NOT NULL,(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='deployment_failed') FROM deployments d JOIN apps p ON p.id=d.app_id WHERE d.id=$1", dep.ID).Scan(&status, &cooldown, &intents); err != nil {
				t.Fatal(err)
			}
			wantInline, wantIntents := int32(0), 0
			wantCooldown := false
			if mode == "off" {
				wantInline = 1
				wantCooldown = true
			}
			if mode == "on" {
				wantIntents = 1
				wantCooldown = true
			}
			if status != "failed" || recorder.calls.Load() != wantInline || intents != wantIntents || cooldown != wantCooldown {
				t.Fatal(mode, status, recorder.calls.Load(), intents, cooldown)
			}
		})
	}
}
