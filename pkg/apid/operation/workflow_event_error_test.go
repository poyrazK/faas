package operation

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowEventInvalidInputReturnsValidationError(t *testing.T) {
	w := httptest.NewRecorder()
	WriteOperationError(w, fmt.Errorf("callback: %w", state.ErrWorkflowInvalidInput))
	var problem api.Problem
	if w.Code != http.StatusBadRequest || json.Unmarshal(w.Body.Bytes(), &problem) != nil || problem.Code != api.CodeValidation {
		t.Fatalf("response: %d %s", w.Code, w.Body)
	}
}
