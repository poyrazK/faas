// adr: 20261017233500
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestSelectedAppTraceRoute(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	id := uuid.MustParse(apidserver.MustSeedApp(t, e, "selected-trace")).String()
	base := "/v1/account/traces/4bf92f3577b34da6a3ce929d0e0e4736/selected-apps"
	for _, test := range []struct {
		query  string
		status int
	}{{"", 400}, {"?app_id=" + id + "&app_id=" + id, 400}, {"?app_id=" + uuid.NewString(), 404}, {"?app_id=" + id + "&limit=1", 200}} {
		r := e.Do(t, http.MethodGet, base+test.query, nil, nil)
		if r.Code != test.status {
			t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
		}
		if r.Code == 200 {
			var out api.SelectedTraceLookupResponse
			if json.Unmarshal(r.Body.Bytes(), &out) != nil || out.Selection != "explicit_app_ids_v1" || len(out.Apps) != 1 || out.Apps[0].ID != id || !out.Partial || out.RequestsStatus != "unavailable" || out.InvocationsStatus != "ok" {
				t.Fatal("native route lost selected identity or unavailable MemStore source")
			}
		}
	}
}
