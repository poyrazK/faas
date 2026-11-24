package object

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// Prod hunt r25: deleting a non-empty bucket said only "Empty the bucket";
// the CLI has no one-step empty, so the hint names the commands that do it.
func TestBucketProblemNotEmptyNamesTheEmptyingCommands(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	BucketProblem(w, objectstorage.ErrNotEmpty)
	var problem api.Problem
	if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if w.Code != 409 || problem.Code != "bucket_not_empty" ||
		!strings.Contains(problem.Hint, "gregale bucket version list") || !strings.Contains(problem.Hint, "gregale bucket deletion create") {
		t.Fatalf("got %d %q hint %q", w.Code, problem.Code, problem.Hint)
	}
}
