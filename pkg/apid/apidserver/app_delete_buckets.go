package apidserver

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// appHasBucketsProblem refuses an app delete that would strand buckets. It
// named neither the buckets nor how to remove them (prod hunt r25), unlike the
// managed PostgreSQL refusal beside it.
func appHasBucketsProblem(slug string, buckets []state.ObjectBucket) *api.Problem {
	names := make([]string, 0, len(buckets))
	for _, bucket := range buckets {
		names = append(names, fmt.Sprintf("%s (%s)", bucket.Name, bucket.ID))
	}
	return api.NewProblem(http.StatusConflict, "app_has_buckets", "Conflict",
		fmt.Sprintf("Delete this app's object storage buckets before deleting the app: %s.", strings.Join(names, ", "))).
		WithHint(fmt.Sprintf("Empty each bucket (gregale bucket version list %[1]s BUCKET_ID lists what it holds; gregale bucket deletion create %[1]s BUCKET_ID KEY REQUEST_UUID deletes an object), then run gregale bucket delete %[1]s BUCKET_ID.", slug))
}
