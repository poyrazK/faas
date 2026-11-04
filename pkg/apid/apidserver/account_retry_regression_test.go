package apidserver_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAccountExportRetryAfterMatchesRemainingWindow(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	st := *e.Store()
	acct := *e.Acct()
	now := time.Now().UTC()
	for _, receipt := range []state.GdprRequest{
		{ID: uuid.NewString(), AccountID: acct.ID, AccountEmail: acct.Email, Action: state.GdprActionExport, RequestedAt: now.Add(-23 * time.Hour)},
		{ID: uuid.NewString(), AccountID: acct.ID, AccountEmail: acct.Email, Action: state.GdprActionRestore, RequestedAt: now.Add(-time.Minute)},
	} {
		if err := st.AppendGdprRequest(t.Context(), receipt); err != nil {
			t.Fatal(err)
		}
	}
	rec := e.Do(t, "GET", "/v1/account/export", nil, nil)
	apidserver.AssertProblem(t, rec, 429, api.CodeExportRateLimited)
	seconds, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || seconds < 3595 || seconds > 3600 {
		t.Fatalf("Retry-After=%q, want remaining hour", rec.Header().Get("Retry-After"))
	}
}
