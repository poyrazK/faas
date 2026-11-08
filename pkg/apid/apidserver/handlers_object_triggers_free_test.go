package apidserver_test

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// Free stores up to 1 GiB (ADR-20261101214152) but has no triggers, so it
// cannot run app code on bucket notifications or compute triggers. Clearing
// them stays allowed, so a downgraded account can remove what it set up.
func TestObjectNotificationsAndComputeTriggersNeedTriggersOnFree(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	(*e.S()).WithObjectStorage(apidserver.ObjectRegistry(t, &apidserver.FakeObjectProvider{}, &apidserver.FakeObjectProvider{}, "external"))
	apidserver.SetS3Flag(t, e, true)
	apidserver.CreateApp(t, e, "free-storage")
	b := apidserver.BucketResponse(t, e.Do(t, "POST", "/v1/apps/free-storage/buckets", map[string]any{"name": "assets"}, nil), 201)
	base := "/v1/apps/free-storage/buckets/" + b.ID
	revision := int64(0)
	for _, tc := range []struct {
		name, path string
		body       map[string]any
		want       int
	}{
		{name: "notification rules", path: base + "/notifications", body: map[string]any{"rules": []map[string]any{{}}}, want: http.StatusPaymentRequired},
		{name: "compute triggers", path: base + "/compute-triggers", body: map[string]any{"expected_revision": revision, "triggers": []map[string]any{{}}}, want: http.StatusPaymentRequired},
		{name: "clearing notifications", path: base + "/notifications", body: map[string]any{"rules": []map[string]any{}}, want: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := e.Do(t, http.MethodPut, tc.path, tc.body, nil)
			if tc.want == http.StatusPaymentRequired {
				apidserver.AssertProblem(t, r, tc.want, api.CodePlanTriggersNotAllowed)
				return
			}
			if r.Code != tc.want {
				t.Fatalf("%s = %d %s, want %d", tc.name, r.Code, r.Body.String(), tc.want)
			}
		})
	}
}
