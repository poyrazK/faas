package apidserver_test

// adr: 732
// A fork's access token is shown once and stored only as its SHA-256. The
// Idempotency-Key layer must not keep the plaintext token in its receipt and
// hand it to a retry.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestAppForkAccessTokenIsNeverStoredForReplay(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).WithAppForksEnabled(true)
	apidserver.SeedAppTaskDeployment(t, e, "my-api")
	key := map[string]string{"Idempotency-Key": uuid.NewString()}

	first := e.Do(t, http.MethodPost, "/v1/apps/my-api/forks", api.CreateAppForkRequest{}, key)
	var fork api.AppForkResponse
	if first.Code != http.StatusAccepted || json.Unmarshal(first.Body.Bytes(), &fork) != nil ||
		!strings.HasPrefix(fork.AccessToken, api.ForkAccessTokenPrefix) {
		t.Fatalf("create = %d %s, want 202 with a fork access token", first.Code, first.Body.String())
	}
	if got := first.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("create Cache-Control = %q, want no-store for a one-time credential", got)
	}

	// A retry with the same key runs the request again. The app's one
	// active fork is taken, so it is refused; it must never be a replay that
	// repeats the token.
	retry := e.Do(t, http.MethodPost, "/v1/apps/my-api/forks", api.CreateAppForkRequest{}, key)
	if strings.Contains(retry.Body.String(), fork.AccessToken) {
		t.Fatalf("retry replayed the fork access token: %d %s", retry.Code, retry.Body.String())
	}
	apidserver.AssertProblem(t, retry, http.StatusConflict, api.CodeAppForkLimit)
}
