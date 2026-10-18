//go:build !no_pg

package apidserver_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/state"
)

type outboxInlineRecorder struct{ calls int }

func (r *outboxInlineRecorder) Send(context.Context, apidserver.Message) error { r.calls++; return nil }

func TestProducerOutboxAccountDeletionWiringPreservesDefaultAndAvoidsInlineDuplicate(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "default_off", true: "enabled"}[enabled], func(t *testing.T) {
			env := apidserver.SetupPGHandler(t, api.PlanPro)
			server := *env.S()
			pool := *env.Pool()
			flags := map[string]string{internalmail.EnableEnv: "1", internalmail.ProducersEnableEnv: "1", internalmail.SchedulingEnableEnv: "1", "FAAS_MAIL_TRANSPORT": "internal-producer", "FAAS_MAIL_FROM": "notify@example.test", "FAAS_INTERNAL_MAIL_PRODUCER_ID": "auth", "FAAS_INTERNAL_MAIL_PRODUCER_TOKEN": "private-apid-outbox-test-token-32-bytes"}
			if enabled {
				flags[internalmail.ProducerOutboxEnableEnv] = "1"
			}
			if err := server.ConfigureMailOutbox(t.Context(), pool, func(k string) string { return flags[k] }); err != nil {
				t.Fatal(err)
			}
			recorder := &outboxInlineRecorder{}
			*server.Mailer() = recorder
			for range 2 {
				response := env.Do(t, http.MethodDelete, "/v1/account", nil, nil)
				if response.Code != http.StatusOK {
					t.Fatal("deletion request failed", response.Code, response.Body.String())
				}
			}
			var count int
			if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM platform_mail_producer_outbox WHERE account_id=$1", env.Acct().ID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if enabled && (count != 1 || recorder.calls != 0) {
				t.Fatal("enabled path lost intent or sent inline duplicate", count, recorder.calls)
			}
			if !enabled && (count != 0 || recorder.calls != 1) {
				t.Fatal("default-off behavior changed", count, recorder.calls)
			}
			acct, err := (*env.Store()).AccountByID(t.Context(), env.Acct().ID)
			if err != nil || acct.Status != state.AccountDeletedPending {
				t.Fatal("business transition missing", err)
			}
			response := env.Do(t, http.MethodGet, internalmail.ProducerOutboxPath, nil, nil)
			if response.Code == http.StatusOK {
				t.Fatal("outbox inspection registered on customer listener")
			}
		})
	}
}

func TestProducerOutboxAccountDeletionSaveFailureDoesNotStartDeletion(t *testing.T) {
	env := apidserver.SetupPGHandler(t, api.PlanPro)
	server := *env.S()
	pool := *env.Pool()
	flags := map[string]string{internalmail.ProducerOutboxEnableEnv: "1", internalmail.EnableEnv: "1", internalmail.ProducersEnableEnv: "1", internalmail.SchedulingEnableEnv: "1", "FAAS_MAIL_TRANSPORT": "internal-producer", "FAAS_MAIL_FROM": "notify@example.test", "FAAS_INTERNAL_MAIL_PRODUCER_ID": "auth", "FAAS_INTERNAL_MAIL_PRODUCER_TOKEN": "private-apid-outbox-test-token-32-bytes"}
	if err := server.ConfigureMailOutbox(t.Context(), pool, func(k string) string { return flags[k] }); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT test_failed_notification_save CHECK (attempts<0)"); err != nil {
		t.Fatal(err)
	}
	response := env.Do(t, http.MethodDelete, "/v1/account", nil, nil)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatal("failed intent save did not reject deletion", response.Code)
	}
	acct, err := (*env.Store()).AccountByID(t.Context(), env.Acct().ID)
	if err != nil || acct.Status != state.AccountActive || acct.DeletionRequestedAt != nil {
		t.Fatal("deletion committed without notification intent", err)
	}
}
