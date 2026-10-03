package api

// adr: 477

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCellDeploymentSDKRefusesRedirects(t *testing.T) {
	for _, code := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		for _, multipart := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/source=%t", code, multipart), func(t *testing.T) {
				var redirected atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/recipient" {
						redirected.Add(1)
						w.WriteHeader(http.StatusAccepted)
						return
					}
					http.Redirect(w, r, "/recipient", code)
				}))
				defer srv.Close()
				client := NewClientWithDeployTimeout(srv.URL, "regional-key", time.Minute).SetCompletionCache(nil)
				client.HTTPClient().CheckRedirect = func(*http.Request, []*http.Request) error { return nil }
				client.deployHTTP.CheckRedirect = func(*http.Request, []*http.Request) error { return nil }
				cellID, appID := uuid.NewString(), uuid.NewString()
				var err error
				if multipart {
					_, err = client.DeployCellAppMultipart(t.Context(), cellID, appID, strings.NewReader("source"), "source.tar.gz", "", "", true, "", DeployAnnotations{})
				} else {
					_, err = client.DeployCellApp(t.Context(), cellID, appID, CreateDeploymentRequest{})
				}
				if err == nil || redirected.Load() != 0 {
					t.Fatalf("redirect: err=%v recipient requests=%d", err, redirected.Load())
				}
			})
		}
	}
}

func TestCellDeploymentSDKValidatesIdentityBeforeReadingSource(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer srv.Close()
	client := NewClient(srv.URL, "regional-key").SetCompletionCache(nil)
	for _, id := range []string{"", "app-name", uuid.Nil.String(), "AD6B5B5F-08B6-4C6C-9391-5CC874BAE39B", strings.ReplaceAll(uuid.NewString(), "-", "")} {
		for _, ids := range [][2]string{{id, uuid.NewString()}, {uuid.NewString(), id}} {
			if _, err := client.DeployCellApp(t.Context(), ids[0], ids[1], CreateDeploymentRequest{}); err == nil {
				t.Errorf("invalid tuple accepted: %v", ids)
			}
			if _, err := client.DeployCellAppMultipart(t.Context(), ids[0], ids[1], unreadCellSource{}, "source.tar.gz", "", "", true, "", DeployAnnotations{}); err == nil {
				t.Errorf("invalid upload tuple accepted: %v", ids)
			}
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid identity reached transport")
	}
}

type unreadCellSource struct{}

func (unreadCellSource) Read([]byte) (int, error) {
	panic("invalid tuple must be rejected before source is read")
}
