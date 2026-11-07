package object

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestInventoryRejectsContradictoryCompletion(t *testing.T) {
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<ListBucketResult><IsTruncated>false</IsTruncated><NextContinuationToken>remaining-page</NextContinuationToken></ListBucketResult>`)
	}))
	t.Cleanup(native.Close)
	p, err := objectstorage.NewS3(objectstorage.BackendConfig{ID: "local", Driver: "s3", Region: "us-east-1", Namespace: "fixture", Endpoint: native.URL, S3Region: "us-east-1", PathStyle: true, AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET"}, func(string) string { return "fixture" })
	if err != nil {
		t.Fatal(err)
	}
	scan := state.ObjectInventoryScan{Bytes: 90, Keys: 9, Pages: 1, After: "previous"}
	err = ResumeObjectInventory(t.Context(), p.(objectstorage.ObjectV2Lister), "physical", &scan, nil)
	t.Logf("inventory scan after contradictory response: %+v err=%v", scan, err)
	if !errors.Is(err, objectstorage.ErrUnavailable) || scan.Complete || scan.Bytes != 90 || scan.Keys != 9 || scan.Pages != 1 {
		t.Error("an incomplete native listing can publish a complete undercounted inventory")
	}
}
