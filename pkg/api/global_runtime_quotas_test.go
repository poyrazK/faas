package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestGlobalRuntimeQuotaSDKRefusesRedirectsAndInvalidIdentity(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Redirect(w, r, "/recipient", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "directory-key").SetCompletionCache(nil)
	id := uuid.NewString()
	for _, verb := range []string{"put", "get", "retire"} {
		call := func(id string) error {
			var err error
			switch verb {
			case "put":
				_, err = c.PutGlobalRuntimeQuota(t.Context(), "quota-global", id, PutGlobalRuntimeQuotaRequest{})
			case "get":
				_, err = c.GetGlobalRuntimeQuota(t.Context(), "quota-global", id)
			case "retire":
				_, err = c.RetireGlobalRuntimeQuota(t.Context(), "quota-global", id)
			}
			return err
		}
		before := calls
		if err := call(id); err == nil || calls != before+1 {
			t.Fatalf("%s followed redirect: %v calls=%d", verb, err, calls)
		}
		before = calls
		for _, invalid := range []string{"bad", uuid.Nil.String()} {
			if err := call(invalid); err == nil || calls != before {
				t.Fatalf("%s invalid identity reached network: %v", verb, err)
			}
		}
	}
}
