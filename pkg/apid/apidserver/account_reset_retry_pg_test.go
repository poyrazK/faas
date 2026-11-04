//go:build !no_pg

package apidserver_test

import (
	"bytes"
	"encoding/base64"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/auth"
)

func TestAccountPasswordResetCanRetryAfterStorageFailure(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	st, pool, acct := *e.Store(), *e.Pool(), *e.Acct()
	ctx := t.Context()
	old, err := auth.Encode("original-password-123!")
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetAccountPassword(ctx, acct.ID, old); err != nil {
		t.Fatal(err)
	}
	raw := bytes.Repeat([]byte{92}, 32)
	if err = st.IssueLoginToken(ctx, api.HashToken(raw), acct.ID, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `create function reject_inline_password() returns trigger language plpgsql as $$ begin raise exception 'injected reset failure'; end $$;
create trigger reject_inline_password before insert or update on account_passwords for each row execute function reject_inline_password()`); err != nil {
		t.Fatal(err)
	}
	post := func() *httptest.ResponseRecorder {
		form := url.Values{"token": {base64.RawURLEncoding.EncodeToString(raw)}, "password": {"replacement-password-123!"}}
		req := httptest.NewRequest("POST", "/auth/reset", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		(*e.H()).ServeHTTP(rec, req)
		return rec
	}
	if rec := post(); rec.Code != 500 {
		t.Fatalf("failed reset=%d %s", rec.Code, rec.Body)
	}
	if _, err = pool.Exec(ctx, `drop trigger reject_inline_password on account_passwords`); err != nil {
		t.Fatal(err)
	}
	if rec := post(); rec.Code != 302 {
		t.Fatalf("retry=%d %s", rec.Code, rec.Body)
	}
	stored, err := st.AccountPasswordByAccountID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := auth.Verify(stored, "replacement-password-123!"); err != nil || !ok {
		t.Fatal("password was not reset")
	}
	if rec := post(); rec.Code != 410 {
		t.Fatalf("replay=%d %s", rec.Code, rec.Body)
	}
}
