//go:build !no_pg

package apidserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

const quotaServiceTestToken = "private-quota-warning-command-test-token-32-bytes"

func quotaFlags(t *testing.T) map[string]string {
	t.Helper()
	flags := paymentFlags(t, false)
	flags[internalmail.QuotaWarningOutboxEnableEnv] = "1"
	path := filepath.Join(t.TempDir(), "quota-warning.token")
	if err := os.WriteFile(path, []byte(quotaServiceTestToken), 0o600); err != nil {
		t.Fatal(err)
	}
	flags[api.InternalMailQuotaWarningTokenPathEnv] = path
	return flags
}

func TestQuotaWarningOutboxPrivateCommandBoundaries(t *testing.T) {
	for _, mode := range []string{"atomic", "intent_failure", "receipt_failure", "stamp_failure", "bad_auth", "remote", "duplicate_auth", "unknown_field", "oversize", "off", "late_enable"} {
		t.Run(mode, func(t *testing.T) {
			e := apidserver.SetupPGHandler(t, api.PlanPro)
			s, pool := *e.S(), *e.Pool()
			id := e.Acct().ID
			var at time.Time
			if err := pool.QueryRow(t.Context(), "INSERT INTO usage_minutes(account_id,app_id,instance_id,minute,mb_seconds) VALUES($1,$2,$3,date_trunc('day',clock_timestamp() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC',$4) RETURNING minute", id, uuid.NewString(), uuid.NewString(), int64(api.PlanPro.PlanIncludedGBHours()+1)*1024*3600).Scan(&at); err != nil {
				t.Fatal(err)
			}
			flags := quotaFlags(t)
			if mode == "late_enable" {
				flags[internalmail.QuotaWarningOutboxEnableEnv] = "0"
			}
			getenv := func(k string) string { return flags[k] }
			if err := s.ConfigureMailOutbox(t.Context(), pool, getenv); err != nil {
				t.Fatal(err)
			}
			if mode == "late_enable" {
				flags[internalmail.QuotaWarningOutboxEnableEnv] = "1"
			}
			mux := http.NewServeMux()
			if err := s.MountInternalMailQuotaWarning(mux, "127.0.0.1:9101", getenv); err != nil {
				t.Fatal(err)
			}
			if mode == "off" {
				flags[internalmail.QuotaWarningOutboxEnableEnv] = "0"
			}
			if mode == "intent_failure" {
				if _, err := pool.Exec(t.Context(), "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT reject_private_quota CHECK(template<>'quota_warning')"); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "stamp_failure" {
				if _, err := pool.Exec(t.Context(), "ALTER TABLE accounts ADD CONSTRAINT reject_private_quota_stamp CHECK(last_quota_warning_at IS NULL)"); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "receipt_failure" {
				if _, err := pool.Exec(t.Context(), "ALTER TABLE billing_quota_warnings ADD CONSTRAINT reject_private_quota_receipt CHECK(false)"); err != nil {
					t.Fatal(err)
				}
			}
			body, err := json.Marshal(api.InternalMailQuotaWarningRequest{Day: at, Plan: api.PlanPro, RequestID: uuid.NewString()})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "unknown_field" {
				body = append(body[:len(body)-1], []byte(`,"recipient":"attacker@example.net"}`)...)
			}
			if mode == "oversize" {
				body = []byte(strings.Repeat(" ", api.InternalMailQuotaWarningCommandMaxBytes+1))
			}
			r := httptest.NewRequest(http.MethodPost, "/v1/internal/billing/accounts/"+id+"/quota-warning", bytes.NewReader(body))
			r.RemoteAddr = "127.0.0.1:12345"
			r.Header.Set("Authorization", "Bearer "+quotaServiceTestToken)
			want := http.StatusServiceUnavailable
			switch mode {
			case "atomic":
				want = http.StatusOK
			case "bad_auth":
				r.Header.Set("Authorization", "Bearer wrong")
				want = http.StatusUnauthorized
			case "remote":
				r.RemoteAddr = "192.0.2.5:12345"
				want = http.StatusForbidden
			case "duplicate_auth":
				r.Header.Add("Authorization", "Bearer "+quotaServiceTestToken)
			case "unknown_field", "oversize":
				want = http.StatusBadRequest
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
			var status string
			var stamped bool
			var intents, receipts int
			if err := pool.QueryRow(t.Context(), "SELECT status,last_quota_warning_at IS NOT NULL,(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='quota_warning'),(SELECT count(*) FROM billing_quota_warnings) FROM accounts WHERE id=$1", id).Scan(&status, &stamped, &intents, &receipts); err != nil {
				t.Fatal(err)
			}
			if mode == "atomic" {
				if status != "active" || !stamped || intents != 1 || receipts != 1 {
					t.Fatal(status, intents, receipts)
				}
			} else if status != "active" || stamped || intents != 0 || receipts != 0 {
				t.Fatal("private boundary partially committed", status, intents, receipts)
			}
			// The service credential never grants access through the customer mux.
			public := httptest.NewRecorder()
			s.Handler().ServeHTTP(public, r.Clone(t.Context()))
			if public.Code >= 200 && public.Code < 300 {
				t.Fatal("private command mounted on customer listener")
			}
		})
	}
}

func TestQuotaWarningOutboxLostPrivateReplyAndLeaseRecovery(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	s, pool := *e.S(), *e.Pool()
	flags := quotaFlags(t)
	getenv := func(k string) string { return flags[k] }
	if err := s.ConfigureMailOutbox(t.Context(), pool, getenv); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	if err := s.MountInternalMailQuotaWarning(mux, "127.0.0.1:9101", getenv); err != nil {
		t.Fatal(err)
	}
	id := e.Acct().ID
	var at time.Time
	if err := pool.QueryRow(t.Context(), "INSERT INTO usage_minutes(account_id,app_id,instance_id,minute,mb_seconds) VALUES($1,$2,$3,date_trunc('day',clock_timestamp() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC',$4) RETURNING minute", id, uuid.NewString(), uuid.NewString(), int64(api.PlanPro.PlanIncludedGBHours()+1)*1024*3600).Scan(&at); err != nil {
		t.Fatal(err)
	}
	var drop atomic.Bool
	drop.Store(true)
	operator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/quota-warning") && drop.CompareAndSwap(true, false) {
			captured := httptest.NewRecorder()
			mux.ServeHTTP(captured, r)
			if captured.Code != http.StatusOK {
				t.Error(captured.Code, captured.Body.String())
			}
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		mux.ServeHTTP(w, r)
	}))
	defer operator.Close()
	client, err := api.NewInternalMailQuotaWarningClient(operator.URL, quotaServiceTestToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Probe(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Warn(t.Context(), id, at, api.PlanPro); err == nil {
		t.Fatal("lost reply reported success")
	}
	first, err := client.Warn(t.Context(), id, at, api.PlanPro)
	if err != nil || first.Followup || first.IntentID == "" {
		t.Fatal("live follow-up lease duplicated", first, err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE billing_quota_warnings SET lease_until=clock_timestamp()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	resumed, err := client.Warn(t.Context(), id, at, api.PlanPro)
	if err != nil || !resumed.Followup || resumed.IntentID != first.IntentID {
		t.Fatal("restart lost receipt", resumed, err)
	}
	for range 2 {
		if err := client.Complete(t.Context(), id, resumed.IntentID, resumed.LeaseID); err != nil {
			t.Fatal(err)
		}
	}
	if done, err := client.Warn(t.Context(), id, at, api.PlanPro); err != nil || done.Followup {
		t.Fatal(done, err)
	}
	var n int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM platform_mail_producer_outbox WHERE template='quota_warning'").Scan(&n); err != nil || n != 1 {
		t.Fatal("lost reply duplicated notice", n, err)
	}
}

func TestQuotaWarningOutboxPrivateMountConfiguration(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	s, pool := *e.S(), *e.Pool()
	if err := s.MountInternalMailQuotaWarning(http.NewServeMux(), "0.0.0.0:9101", func(k string) string {
		if k != internalmail.QuotaWarningOutboxEnableEnv {
			t.Fatal("disabled mount read config", k)
		}
		return ""
	}); err != nil {
		t.Fatal(err)
	}
	flags := quotaFlags(t)
	getenv := func(k string) string { return flags[k] }
	if err := s.ConfigureMailOutbox(t.Context(), pool, getenv); err != nil {
		t.Fatal(err)
	}
	if err := s.MountInternalMailQuotaWarning(http.NewServeMux(), "0.0.0.0:9101", getenv); err == nil {
		t.Fatal("public listener accepted")
	}
	for _, name := range []string{"FAAS_CANARY_PROGRESSION_TOKEN", "FAAS_SAFEDEPLOY_TOKEN", "FAAS_INTERNAL_MAIL_PRODUCER_TOKEN"} {
		original := flags[name]
		for _, value := range []string{quotaServiceTestToken, " \n" + quotaServiceTestToken + "\n "} {
			flags[name] = value
			if err := s.MountInternalMailQuotaWarning(http.NewServeMux(), "127.0.0.1:9101", getenv); err == nil {
				t.Fatal("sibling service credential reused", name)
			}
		}
		flags[name] = original
	}
	flags[internalmail.AccountSuspendedOutboxEnableEnv] = "1"
	flags[api.InternalMailSuspensionTokenPathEnv] = flags[api.InternalMailQuotaWarningTokenPathEnv]
	if err := s.MountInternalMailQuotaWarning(http.NewServeMux(), "127.0.0.1:9101", getenv); err == nil {
		t.Fatal("suspension credential reused for quota warnings")
	}
}

func TestQuotaWarningOutboxNonpaymentCredentialMustBeDistinct(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	s, pool := *e.S(), *e.Pool()
	flags := quotaFlags(t)
	getenv := func(k string) string { return flags[k] }
	if err := s.ConfigureMailOutbox(t.Context(), pool, getenv); err != nil {
		t.Fatal(err)
	}
	flags[internalmail.NonpaymentDeletionOutboxEnableEnv] = "1"
	flags[api.InternalMailNonpaymentDeletionTokenPathEnv] = flags[api.InternalMailQuotaWarningTokenPathEnv]
	if err := s.MountInternalMailQuotaWarning(http.NewServeMux(), "127.0.0.1:9101", getenv); err == nil {
		t.Fatal("non-payment credential reused for quota warnings")
	}
}
