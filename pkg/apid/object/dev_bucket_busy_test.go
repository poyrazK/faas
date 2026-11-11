package object

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDevBucketBusyProblemNamesPendingUploads(t *testing.T) {
	issued := time.Date(2026, 11, 9, 20, 35, 10, 0, time.UTC)
	bucket := state.ObjectBucket{ID: "92ca01de-281a-4289-9dec-86b1f9546aef", Name: "probe"}
	pending := []api.ObjectWriteReceipt{
		{Key: "a1.txt", CreatedAt: issued},
		{Key: "a2.txt", CreatedAt: issued.Add(2 * time.Minute)},
		{Key: "a3.txt", CreatedAt: issued},
		{Key: "a4.txt", CreatedAt: issued},
	}
	p := devBucketBusyProblem("dev-probe-1", bucket, pending)
	if p.Status != http.StatusConflict || p.Code != "object_storage_conflict" {
		t.Fatalf("problem = %+v", p)
	}
	for _, want := range []string{
		`Bucket "probe" has 4 pending upload(s)`,
		"a1.txt, a2.txt, a3.txt and 1 more",
		"Retry after 20:52 UTC", // latest issue 20:37:10 + 15 min
		"gregale bucket writes list dev-probe-1 92ca01de-281a-4289-9dec-86b1f9546aef",
	} {
		if !strings.Contains(p.Detail, want) {
			t.Errorf("detail lacks %q:\n%s", want, p.Detail)
		}
	}
}
