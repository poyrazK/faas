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

const nonpaymentServiceTestToken = "private-nonpayment-deletion-command-test-token-32-bytes"

func nonpaymentFlags(t *testing.T) map[string]string {
	t.Helper()
	flags := paymentFlags(t, false)
	flags[internalmail.NonpaymentDeletionOutboxEnableEnv] = "1"
	path := filepath.Join(t.TempDir(), "nonpayment-deletion.token")
	if err := os.WriteFile(path, []byte(nonpaymentServiceTestToken), 0o600); err != nil {
		t.Fatal(err)
	}
	flags[api.InternalMailNonpaymentDeletionTokenPathEnv] = path
	return flags
}

func TestNonpaymentDeletionOutboxPrivateCommandBoundaries(t *testing.T) {
	for _, mode := range []string{"atomic", "intent_failure", "receipt_failure", "bad_auth", "remote", "duplicate_auth", "unknown_field", "oversize", "off", "late_enable"} {
		t.Run(mode, func(t *testing.T) {
			e := apidserver.SetupPGHandler(t, api.PlanPro)
			s, pool := *e.S(), *e.Pool()
			id := e.Acct().ID
			var at time.Time
			if err := pool.QueryRow(t.Context(), "UPDATE accounts SET status='suspended',past_due_at=clock_timestamp()-interval '22 days' WHERE id=$1 RETURNING past_due_at", id).Scan(&at); err != nil {
				t.Fatal(err)
			}
			flags := nonpaymentFlags(t)
			if mode == "late_enable" {
				flags[internalmail.NonpaymentDeletionOutboxEnableEnv] = "0"
			}
			getenv := func(k string) string { return flags[k] }
			if err := s.ConfigureMailOutbox(t.Context(), pool, getenv); err != nil {
				t.Fatal(err)
			}
			if mode == "late_enable" {
				flags[internalmail.NonpaymentDeletionOutboxEnableEnv] = "1"
			}
			mux := http.NewServeMux()
			if err := s.MountInternalMailNonpaymentDeletion(mux, "127.0.0.1:9101", getenv); err != nil {
				t.Fatal(err)
			}
			if mode == "off" {
				flags[internalmail.NonpaymentDeletionOutboxEnableEnv] = "0"
			}
			if mode == "intent_failure" {
				if _, err := pool.Exec(t.Context(), "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT reject_private_suspension CHECK(template<>'nonpayment_deletion')"); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "receipt_failure" {
				if _, err := pool.Exec(t.Context(), "ALTER TABLE billing_account_nonpayment_deletions ADD CONSTRAINT reject_private_suspension_receipt CHECK(false)"); err != nil {
					t.Fatal(err)
				}
			}
			body, err := json.Marshal(api.InternalMailNonpaymentDeletionRequest{PastDueAt: at, RequestID: uuid.NewString()})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "unknown_field" {
				body = append(body[:len(body)-1], []byte(`,"recipient":"attacker@example.net"}`)...)
			}
			if mode == "oversize" {
				body = []byte(strings.Repeat(" ", api.InternalMailNonpaymentDeletionCommandMaxBytes+1))
			}
			r := httptest.NewRequest(http.MethodPost, "/v1/internal/billing/accounts/"+id+"/nonpayment-deletion", bytes.NewReader(body))
			r.RemoteAddr = "127.0.0.1:12345"
			r.Header.Set("Authorization", "Bearer "+nonpaymentServiceTestToken)
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
				r.Header.Add("Authorization", "Bearer "+nonpaymentServiceTestToken)
			case "unknown_field", "oversize":
				want = http.StatusBadRequest
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
			var status string
			var intents, receipts int
			if err := pool.QueryRow(t.Context(), "SELECT status,(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='nonpayment_deletion'),(SELECT count(*) FROM billing_account_nonpayment_deletions) FROM accounts WHERE id=$1", id).Scan(&status, &intents, &receipts); err != nil {
				t.Fatal(err)
			}
			if mode == "atomic" {
				if status != "deleted_pending" || intents != 1 || receipts != 1 {
					t.Fatal(status, intents, receipts)
				}
			} else if status != "suspended" || intents != 0 || receipts != 0 {
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

func TestNonpaymentDeletionOutboxLostPrivateReplyAndLeaseRecovery(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	s, pool := *e.S(), *e.Pool()
	flags := nonpaymentFlags(t)
	getenv := func(k string) string { return flags[k] }
	if err := s.ConfigureMailOutbox(t.Context(), pool, getenv); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	if err := s.MountInternalMailNonpaymentDeletion(mux, "127.0.0.1:9101", getenv); err != nil {
		t.Fatal(err)
	}
	id := e.Acct().ID
	var at time.Time
	if err := pool.QueryRow(t.Context(), "UPDATE accounts SET status='suspended',past_due_at=clock_timestamp()-interval '22 days' WHERE id=$1 RETURNING past_due_at", id).Scan(&at); err != nil {
		t.Fatal(err)
	}
	var drop atomic.Bool
	drop.Store(true)
	operator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/nonpayment-deletion") && drop.CompareAndSwap(true, false) {
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
	client, err := api.NewInternalMailNonpaymentDeletionClient(operator.URL, nonpaymentServiceTestToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Probe(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Schedule(t.Context(), id, at); err == nil {
		t.Fatal("lost reply reported success")
	}
	first, err := client.Schedule(t.Context(), id, at)
	if err != nil || first.Followup || first.IntentID == "" {
		t.Fatal("live follow-up lease duplicated", first, err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE billing_account_nonpayment_deletions SET lease_until=clock_timestamp()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	resumed, err := client.Schedule(t.Context(), id, at)
	if err != nil || !resumed.Followup || resumed.IntentID != first.IntentID {
		t.Fatal("restart lost receipt", resumed, err)
	}
	for range 2 {
		if err := client.Complete(t.Context(), id, resumed.IntentID, resumed.LeaseID); err != nil {
			t.Fatal(err)
		}
	}
	if done, err := client.Schedule(t.Context(), id, at); err != nil || done.Followup {
		t.Fatal(done, err)
	}
	var n int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM platform_mail_producer_outbox WHERE template='nonpayment_deletion'").Scan(&n); err != nil || n != 1 {
		t.Fatal("lost reply duplicated notice", n, err)
	}
}

func TestNonpaymentDeletionOutboxPrivateMountConfiguration(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	s, pool := *e.S(), *e.Pool()
	if err := s.MountInternalMailNonpaymentDeletion(http.NewServeMux(), "0.0.0.0:9101", func(k string) string {
		if k != internalmail.NonpaymentDeletionOutboxEnableEnv {
			t.Fatal("disabled mount read config", k)
		}
		return ""
	}); err != nil {
		t.Fatal(err)
	}
	flags := nonpaymentFlags(t)
	getenv := func(k string) string { return flags[k] }
	if err := s.ConfigureMailOutbox(t.Context(), pool, getenv); err != nil {
		t.Fatal(err)
	}
	if err := s.MountInternalMailNonpaymentDeletion(http.NewServeMux(), "0.0.0.0:9101", getenv); err == nil {
		t.Fatal("public listener accepted")
	}
	for _, name := range []string{"FAAS_CANARY_PROGRESSION_TOKEN", "FAAS_SAFEDEPLOY_TOKEN", "FAAS_INTERNAL_MAIL_PRODUCER_TOKEN"} {
		original := flags[name]
		for _, value := range []string{nonpaymentServiceTestToken, " \n" + nonpaymentServiceTestToken + "\n "} {
			flags[name] = value
			if err := s.MountInternalMailNonpaymentDeletion(http.NewServeMux(), "127.0.0.1:9101", getenv); err == nil {
				t.Fatal("sibling service credential reused", name)
			}
		}
		flags[name] = original
	}
	flags[internalmail.AccountSuspendedOutboxEnableEnv] = "1"
	flags[api.InternalMailSuspensionTokenPathEnv] = flags[api.InternalMailNonpaymentDeletionTokenPathEnv]
	if err := s.MountInternalMailNonpaymentDeletion(http.NewServeMux(), "127.0.0.1:9101", getenv); err == nil {
		t.Fatal("suspension credential reused for deletion")
	}
}
