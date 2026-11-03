package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// racingOpenAPIDocStore captures one more deployment's doc, as a concurrent
// request would, right after the first count of the account's docs.
type racingOpenAPIDocStore struct {
	MemStore *state.MemStore
	race     func()
}

func (s *racingOpenAPIDocStore) CountOpenAPIDocsByAccount(ctx context.Context, accountID string) (int, error) {
	n, err := s.MemStore.CountOpenAPIDocsByAccount(ctx, accountID)
	if race := s.race; race != nil {
		s.race = nil
		race()
	}
	return n, err
}

// Two requests that capture an account's last OpenAPI document cannot both
// get one: the quota is counted and the document saved as one step. Here
// another request's document lands between this request's count and its
// save.
func TestOpenAPIDocQuotaHoldsAgainstAConcurrentCapture(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	ctx, store, acct := t.Context(), *e.Store(), *e.Acct()
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "docs-api", Type: state.AppTypeApp,
		Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	limit := api.PlanHobby.OpenAPIDocsPerAccount()
	doc := []byte(`{"openapi":"3.1.0","info":{"title":"docs"}}`)
	deployments := make([]string, limit+1)
	for i := range deployments {
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:test"})
		if err != nil {
			t.Fatal(err)
		}
		deployments[i] = d.ID
	}
	capture := func(deploymentID string) {
		t.Helper()
		if err := store.UpsertDeploymentOpenAPIDoc(ctx, deploymentID, acct.ID, app.ID, doc, state.OpenAPIDocSourceManualUpload, false); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range deployments[:limit-1] {
		capture(id)
	}
	concurrent, target := deployments[limit-1], deployments[limit]
	(*(*e.S()).Store()) = statetest.NewMem(&racingOpenAPIDocStore{MemStore: store, race: func() { capture(concurrent) }})

	res := e.Do(t, http.MethodPatch, "/v1/apps/docs-api/deployments/"+target+"/openapi",
		map[string]any{"set_doc": true, "doc": json.RawMessage(doc)}, nil)
	count, err := store.CountOpenAPIDocsByAccount(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	refused := res.Code == http.StatusForbidden && strings.Contains(res.Body.String(), api.CodePlanOpenAPIDocQuotaReached)
	if count > limit || res.Code != http.StatusOK && !refused {
		t.Fatalf("the account holds %d OpenAPI documents against a cap of %d after the capture answered %d %s", count, limit, res.Code, res.Body)
	}
}
