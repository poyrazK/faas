package apidserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectRollbackExpiredProblem(t *testing.T) {
	server := &server{}
	response := httptest.NewRecorder()
	if !server.writeProjectControlError(response, fmt.Errorf("control: %w", state.ErrProjectReleaseBaselineExpired)) {
		t.Fatal("expiry error not handled")
	}
	var problem api.Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusGone || problem.Code != "project_release_baseline_expired" || problem.Detail == "" {
		t.Fatalf("expiry problem: %+v", problem)
	}
}
