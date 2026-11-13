package objectstorage

import (
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 553 — upload routes perform one PUT, regardless of encryption or
// whether the registry permits larger objects through multipart uploads.
func TestUploadHandlerSinglePutLimit(t *testing.T) {
	for _, tracked := range []bool{false, true} {
		for _, body := range []string{"four", "avatar"} {
			t.Run(map[bool]string{false: "legacy", true: "tracked"}[tracked]+"/"+body, func(t *testing.T) {
				var f uploadFixture
				if tracked {
					f, _ = newTrackedUploadFixture(t)
				} else {
					f = newUploadFixture(t)
				}
				f.registry.MaxSinglePutBytes = 4
				res := f.request(http.MethodPost, "/uploads/avatar", body, "image/png")
				if len(body) == 4 {
					if res.Code != http.StatusCreated || f.provider.writes != 1 {
						t.Fatalf("at limit = %d %s, writes=%d", res.Code, res.Body.String(), f.provider.writes)
					}
					return
				}
				if res.Code != http.StatusRequestEntityTooLarge || f.provider.writes != 0 {
					t.Fatalf("over limit = %d %s, writes=%d", res.Code, res.Body.String(), f.provider.writes)
				}
				for _, want := range []string{"limit 4 bytes", "observed 6", api.ObjectStorageLimitsDocsURL} {
					if !strings.Contains(res.Body.String(), want) {
						t.Errorf("size refusal omitted %q: %s", want, res.Body.String())
					}
				}
			})
		}
	}
}
