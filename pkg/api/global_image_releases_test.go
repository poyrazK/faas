package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestGlobalImageReleaseSDKRedirectAndSerialization(t *testing.T) {
	id := uuid.NewString()
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/global-apps/global-shop/image-releases/"+id {
			t.Error("unexpected path")
		}
		var req PutGlobalImageReleaseRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.RegionalAPIKeys["eu-fsn"] != "regional-secret" {
			t.Error("request mismatch")
		}
		http.Redirect(w, r, "/recipient", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client := NewClient(server.URL, "directory-key").SetCompletionCache(nil)
	_, err := client.PutGlobalImageRelease(context.Background(), "global-shop", id, PutGlobalImageReleaseRequest{Image: "image", RegionalAPIKeys: map[string]string{"eu-fsn": "regional-secret"}})
	if err == nil || calls != 1 {
		t.Fatalf("redirect: %v calls=%d", err, calls)
	}
	calls = 0
	for _, invalid := range []string{"bad", uuid.Nil.String()} {
		if _, err := client.PutGlobalImageRelease(t.Context(), "global-shop", invalid, PutGlobalImageReleaseRequest{}); err == nil {
			t.Fatal("invalid release accepted")
		}
	}
	if calls != 0 {
		t.Fatal("invalid identity reached network")
	}
}
