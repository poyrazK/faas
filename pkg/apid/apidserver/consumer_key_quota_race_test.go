package apidserver_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// racingConsumerKeyStore issues one more key, as a concurrent request
// would, right after the first read of an app's keys.
type racingConsumerKeyStore struct {
	MemStore *state.MemStore
	race     func()
}

func (s *racingConsumerKeyStore) ListConsumerKeysForApp(ctx context.Context, accountID, appID string) ([]state.ConsumerKey, error) {
	rows, err := s.MemStore.ListConsumerKeysForApp(ctx, accountID, appID)
	if race := s.race; race != nil {
		s.race = nil
		race()
	}
	return rows, err
}

// Two requests that issue an app's last consumer key cannot both get one:
// the key quota is counted and the key inserted as one step. Here another
// request's key lands between this request's read of the app's keys and its
// insert.
func TestConsumerKeyQuotaHoldsAgainstAConcurrentIssue(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	ctx, store, acct := t.Context(), *e.Store(), *e.Acct()
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "keys-api", Type: state.AppTypeApp,
		Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := store.CreateAPIConsumer(ctx, acct.ID, app.ID, "ref-keys", "keys")
	if err != nil {
		t.Fatal(err)
	}
	issue := func(name string) {
		t.Helper()
		_, prefix, hash, err := api.GenerateConsumerKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateConsumerKeyForConsumer(ctx, acct.ID, consumer.ID, name, prefix, hash, []string{"read"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	perApp := api.PlanHobby.ConsumerKeysPerApp()
	for i := range perApp - 1 {
		issue(fmt.Sprintf("key-%d", i))
	}
	(*(*e.S()).Store()) = statetest.NewMem(&racingConsumerKeyStore{MemStore: store, race: func() { issue("concurrent") }})

	res := e.Do(t, http.MethodPost, "/v1/apps/keys-api/consumers/"+consumer.ID+"/keys",
		api.CreateConsumerKeyRequest{Name: "new", Scopes: []string{"read"}}, nil)
	keys, err := store.ListConsumerKeysForApp(ctx, acct.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	refused := res.Code == http.StatusForbidden && strings.Contains(res.Body.String(), api.CodePlanConsumerKeyQuotaReached)
	if len(keys) > perApp || res.Code != http.StatusCreated && !refused {
		t.Fatalf("the app holds %d keys against a cap of %d after the issue answered %d %s", len(keys), perApp, res.Code, res.Body)
	}
}
