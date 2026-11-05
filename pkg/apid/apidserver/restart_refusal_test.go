package apidserver_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Hunt 14 H14-83: `secrets set` suggests restart --fresh, which a parked app
// refused with only "only an active app can be restarted". A parked app's
// next wake already boots with the current secrets, so the refusal says so.
func TestAppRestartRefusalExplainsParkedApps(t *testing.T) {
	parked := apidserver.AppRestartRefusal(state.App{Slug: "h14-py", Status: state.AppEvictedCold})
	if parked.Status != http.StatusConflict || parked.Code != api.CodeConflict {
		t.Fatalf("parked refusal = %d/%s, want 409/%s", parked.Status, parked.Code, api.CodeConflict)
	}
	for _, want := range []string{"h14-py is parked", "next request or wake boots it with the current secrets"} {
		if !strings.Contains(parked.Detail, want) {
			t.Fatalf("parked detail %q lacks %q", parked.Detail, want)
		}
	}
	if !strings.Contains(parked.Hint, "gregale wake h14-py") {
		t.Fatalf("parked hint %q lacks the wake command", parked.Hint)
	}
	other := apidserver.AppRestartRefusal(state.App{Slug: "gone", Status: state.AppDeleted})
	if other.Status != http.StatusConflict || other.Detail != "only an active app can be restarted" {
		t.Fatalf("non-parked refusal = %d %q", other.Status, other.Detail)
	}
}
