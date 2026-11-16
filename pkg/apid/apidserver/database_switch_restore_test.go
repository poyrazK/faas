// adr: 20261023181931 — amendment 3: a restore may switch the app to the copy and keep its code.
package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// restorePoint picks a point inside the fixture database's restore range.
func restorePoint(t *testing.T, f switchFixture) string {
	t.Helper()
	source, err := (*(*f.env.Srv()).ManagedPostgres()).Get(context.Background(), *f.env.AcctID(), f.sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, latest, ok := source.RestoreRange(time.Now().UTC()); ok {
		return latest.Add(-time.Second).UTC().Format(time.RFC3339Nano)
	}
	return time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
}

func TestRestoreSwitchRecordsASwitchThatKeepsTheLiveRelease(t *testing.T) {
	f := newSwitchFixture(t)
	path := "/v1/apps/" + f.slug + "/database-switches"
	w := f.do(t, http.MethodPost, path, api.CreateDatabaseSwitchRequest{DatabaseID: f.sourceID, Name: "orders-0900", PointInTime: restorePoint(t, f)})
	if w.Code != http.StatusAccepted {
		t.Fatalf("restore switch = %d %s, want 202", w.Code, w.Body)
	}
	var sw api.DatabaseSwitch
	if err := json.Unmarshal(w.Body.Bytes(), &sw); err != nil {
		t.Fatal(err)
	}
	if sw.Phase != "restoring" || !sw.KeepsRelease || sw.FromDeploymentID != f.v3.ID || sw.TargetDeploymentID != f.v3.ID ||
		len(sw.UndoneDeploymentIDs) != 0 || sw.SourceDatabaseID != f.sourceID || sw.TargetName != "orders-0900" {
		t.Fatalf("switch %+v", sw)
	}
	live, err := (*f.env.Store()).DeploymentByID(context.Background(), f.v3.ID)
	if err != nil || live.Status != state.DeployLive {
		t.Fatalf("the request changed the live release: %s (%v)", live.Status, err)
	}
	w = f.do(t, http.MethodPost, path, api.CreateDatabaseSwitchRequest{DatabaseID: f.sourceID, Name: "orders-0901", PointInTime: restorePoint(t, f)})
	if w.Code != http.StatusConflict || switchProblemCode(t, w) != api.CodeDatabaseSwitchInProgress {
		t.Fatalf("second switch = %d %s", w.Code, w.Body)
	}
}

func TestRestoreSwitchRefusals(t *testing.T) {
	f := newSwitchFixture(t)
	path := "/v1/apps/" + f.slug + "/database-switches"
	point := restorePoint(t, f)
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	for _, tt := range []struct {
		name string
		req  api.CreateDatabaseSwitchRequest
		code int
		want string
	}{
		{"no database", api.CreateDatabaseSwitchRequest{Name: "copy", PointInTime: point}, http.StatusBadRequest, api.CodeValidation},
		{"no name", api.CreateDatabaseSwitchRequest{DatabaseID: f.sourceID, PointInTime: point}, http.StatusBadRequest, api.CodeValidation},
		{"bad point", api.CreateDatabaseSwitchRequest{DatabaseID: f.sourceID, Name: "copy", PointInTime: "yesterday"}, http.StatusBadRequest, api.CodeValidation},
		{"future point", api.CreateDatabaseSwitchRequest{DatabaseID: f.sourceID, Name: "copy", PointInTime: future}, http.StatusConflict, api.CodeDatabaseSwitchUnavailable},
		{"unknown database", api.CreateDatabaseSwitchRequest{DatabaseID: "00000000-0000-4000-8000-000000000000", Name: "copy", PointInTime: point}, http.StatusNotFound, api.CodeNotFound},
		{"scope without a live release", api.CreateDatabaseSwitchRequest{DatabaseID: f.sourceID, Name: "copy", PointInTime: point, Scope: "staging"}, http.StatusConflict, api.CodeDatabaseSwitchUnavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := f.do(t, http.MethodPost, path, tt.req)
			if w.Code != tt.code || switchProblemCode(t, w) != tt.want {
				t.Fatalf("= %d %s, want %d %s", w.Code, w.Body, tt.code, tt.want)
			}
		})
	}
	w := f.do(t, http.MethodGet, path, nil)
	var list api.DatabaseSwitchList
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Items) != 0 {
		t.Fatalf("a refused switch was recorded: %d %s", w.Code, w.Body)
	}
}
