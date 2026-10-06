package api

// adr: 647
import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestGlobalQuotaDeliverySDKRejectsRedirectsAndInvalidIDs(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Redirect(w, r, "/recipient", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "directory-key").SetCompletionCache(nil)
	allocation, id := uuid.NewString(), uuid.NewString()
	for _, verb := range []string{"queue", "show", "retry"} {
		call := func(delivery string) error {
			var err error
			switch verb {
			case "queue":
				_, err = c.QueueGlobalQuotaDelivery(t.Context(), "quota-global", allocation, delivery, QueueGlobalQuotaDeliveryRequest{})
			case "show":
				_, err = c.GetGlobalQuotaDelivery(t.Context(), "quota-global", allocation, delivery)
			case "retry":
				_, err = c.RetryGlobalQuotaDelivery(t.Context(), "quota-global", allocation, delivery)
			}
			return err
		}
		before := calls
		if err := call(id); err == nil || calls != before+1 {
			t.Fatal("redirect followed", verb, err, calls)
		}
		before = calls
		for _, bad := range []string{"bad", uuid.Nil.String()} {
			if err := call(bad); err == nil || calls != before {
				t.Fatal("invalid identity reached network", verb, err)
			}
		}
	}
}
