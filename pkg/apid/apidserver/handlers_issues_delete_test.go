//go:build !no_pg

package apidserver_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestIssueDeleteIsAppScopedAndReleasesIdentity(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	app := apidserver.SeedPGApp(t, e, "issue-delete")
	other := apidserver.SeedPGApp(t, e, "issue-delete-other")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error", Message: "failure"}
	original := apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, event), http.StatusAccepted)
	if w := e.Do(t, "DELETE", "/v1/apps/"+other.Slug+"/issues/"+original.IssueID, nil, nil); w.Code != http.StatusNotFound {
		t.Fatalf("cross-app delete: %d %s", w.Code, w.Body.String())
	}
	path := "/v1/apps/" + app.Slug + "/issues/" + original.IssueID
	if w := e.Do(t, "DELETE", path, nil, nil); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if w := e.Do(t, "GET", path, nil, nil); w.Code != http.StatusNotFound {
		t.Fatalf("deleted issue still visible: %d", w.Code)
	}
	event.EventID = uuid.NewString()
	replacement := apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, event), http.StatusAccepted)
	if replacement.IssueID == original.IssueID {
		t.Fatal("deleted identity was reused")
	}
}

func TestIssueDeleteRequiresExplicitExpiredTicketAbandonment(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	app := apidserver.SeedPGApp(t, e, "issue-delete-abandon")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error", Message: "failure"}
	original := apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, event), http.StatusAccepted)
	acct := apidserver.IssueNativeAccount(e)
	store := state.NewPgStore(*e.Pool())
	receipt, err := store.ReserveNativeIssueTicket(t.Context(), state.NativeIssueTicketReceipt{AccountID: acct.ID, AppID: app.ID, IssueID: original.IssueID, RequestID: uuid.NewString(), Hash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimNativeIssueTicket(t.Context(), receipt, false); err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/" + app.Slug + "/issues/" + original.IssueID
	if w := e.Do(t, "DELETE", path+"?abandon_unconfirmed_tickets=true", nil, nil); w.Code != http.StatusConflict {
		t.Fatalf("active dispatch: %d %s", w.Code, w.Body.String())
	}
	if _, err := (*e.Pool()).Exec(t.Context(), `UPDATE native_issue_ticket_receipts SET recover_after=now()-interval '1 second' WHERE request_id=$1`, receipt.RequestID); err != nil {
		t.Fatal(err)
	}
	if w := e.Do(t, "DELETE", path, nil, nil); w.Code != http.StatusConflict {
		t.Fatalf("default deletion: %d %s", w.Code, w.Body.String())
	}
	if w := e.Do(t, "DELETE", path+"?abandon_unconfirmed_tickets=invalid", nil, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid flag: %d %s", w.Code, w.Body.String())
	}
	if w := e.Do(t, "DELETE", path+"?abandon_unconfirmed_tickets=true", nil, nil); w.Code != http.StatusNoContent {
		t.Fatalf("explicit abandonment: %d %s", w.Code, w.Body.String())
	}
}
