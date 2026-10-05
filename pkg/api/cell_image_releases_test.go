package api

// adr: 604

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

func TestCellImageReleaseOrderingBounds(t *testing.T) {
	for _, value := range []CellImageReleaseOrdering{{GlobalAppID: uuid.NewString(), Generation: 1}, {GlobalAppID: uuid.NewString(), Generation: GlobalImageReleaseGenerationMax}} {
		if !value.Valid() {
			t.Fatalf("valid ordering rejected: %+v", value)
		}
		body, err := json.Marshal(PutCellImageReleaseRequest{Image: "image", Ordering: &value})
		if err != nil {
			t.Fatal(err)
		}
		var decoded PutCellImageReleaseRequest
		if err := json.Unmarshal(body, &decoded); err != nil || decoded.Ordering == nil || *decoded.Ordering != value {
			t.Fatalf("generation lost JSON precision: %+v %v", decoded, err)
		}
	}
	for _, value := range []CellImageReleaseOrdering{{GlobalAppID: uuid.Nil.String(), Generation: 1}, {GlobalAppID: uuid.NewString(), Generation: 0}, {GlobalAppID: uuid.NewString(), Generation: GlobalImageReleaseGenerationMax + 1}} {
		if value.Valid() {
			t.Fatalf("invalid ordering accepted: %+v", value)
		}
	}
	if StatusForCode(CodeRegionalImageReleaseOrder) != http.StatusConflict {
		t.Fatal("ordering conflict status")
	}
}

func TestCellImageReleaseSDKRefusesRedirects(t *testing.T) {
	for _, method := range []string{http.MethodPut, http.MethodGet} {
		for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
			t.Run(fmt.Sprintf("%s/%d", method, status), func(t *testing.T) {
				var redirected atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/recipient" {
						redirected.Add(1)
						return
					}
					http.Redirect(w, r, "/recipient", status)
				}))
				defer srv.Close()
				client := NewClient(srv.URL, "regional-key").SetCompletionCache(nil)
				client.HTTPClient().CheckRedirect = func(*http.Request, []*http.Request) error { return nil }
				var err error
				if method == http.MethodPut {
					_, err = client.PutCellImageRelease(t.Context(), uuid.NewString(), uuid.NewString(), uuid.NewString(), PutCellImageReleaseRequest{Image: "image"})
				} else {
					_, err = client.GetCellImageRelease(t.Context(), uuid.NewString(), uuid.NewString(), uuid.NewString())
				}
				if err == nil || redirected.Load() != 0 {
					t.Fatalf("redirect: %v recipient requests=%d", err, redirected.Load())
				}
			})
		}
	}
}

func TestCellImageReleaseSDKRejectsMalformedIdentity(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer srv.Close()
	client := NewClient(srv.URL, "regional-key").SetCompletionCache(nil)
	for _, invalid := range []string{"", uuid.Nil.String(), strings.ReplaceAll(uuid.NewString(), "-", ""), "AD6B5B5F-08B6-4C6C-9391-5CC874BAE39B"} {
		for index := range 3 {
			ids := [3]string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
			ids[index] = invalid
			if _, err := client.PutCellImageRelease(t.Context(), ids[0], ids[1], ids[2], PutCellImageReleaseRequest{}); err == nil {
				t.Fatal("malformed PUT identity accepted")
			}
			if _, err := client.GetCellImageRelease(t.Context(), ids[0], ids[1], ids[2]); err == nil {
				t.Fatal("malformed GET identity accepted")
			}
		}
	}
	if requests.Load() != 0 {
		t.Fatal("malformed identity reached the transport")
	}
}
