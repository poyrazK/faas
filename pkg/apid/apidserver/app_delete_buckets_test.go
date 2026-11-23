package apidserver_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Prod hunt r25: an app delete refused for its buckets named neither the
// buckets nor the commands that remove them.
func TestAppHasBucketsProblemNamesBucketsAndCommands(t *testing.T) {
	p := apidserver.AppHasBucketsProblem("shop", []state.ObjectBucket{{ID: "b-1", Name: "files"}})
	if p.Status != 409 || p.Code != "app_has_buckets" || !strings.Contains(p.Detail, "files (b-1)") {
		t.Fatalf("problem = %+v", p)
	}
	for _, want := range []string{"gregale bucket version list shop", "gregale bucket deletion create shop", "gregale bucket delete shop BUCKET_ID"} {
		if !strings.Contains(p.Hint, want) {
			t.Errorf("hint %q lacks %q", p.Hint, want)
		}
	}
}
