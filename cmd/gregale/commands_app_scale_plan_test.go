package main_test

import (
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Hunt #9 H9-75: UpdateAppRequest.StartCommand carried an empty json tag, so
// every marshalled request held "StartCommand":null and `gregale app <slug>
// scale --plan` failed for every flag with `no preview label for changed
// field "StartCommand"`. The preview must list exactly the settings the
// flags changed.
func TestBuildAppScalePlanChangesListsOnlyRequestedSettings(t *testing.T) {
	two, ram, idle := 2, 256, 120
	tru := true
	app := api.AppResponse{Slug: "echo", RAMMB: 128, MaxConcurrency: 1}
	for _, tc := range []struct {
		name    string
		request api.UpdateAppRequest
		want    string
	}{
		{name: "max concurrency", request: api.UpdateAppRequest{MaxConcurrency: &two}, want: "max_concurrency"},
		{name: "ram", request: api.UpdateAppRequest{RAMMB: &ram}, want: "ram_mb"},
		{name: "idle timeout", request: api.UpdateAppRequest{IdleTimeoutS: &idle}, want: "idle_timeout_s"},
		{name: "require authn", request: api.UpdateAppRequest{RequireAuthn: &tru}, want: "require_authn"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changes, err := main.BuildAppScalePlanChanges(app, tc.request)
			if err != nil {
				t.Fatalf("buildAppScalePlanChanges: %v", err)
			}
			if len(changes) != 1 || changes[0].Field != tc.want {
				t.Fatalf("changes = %+v, want exactly %q", changes, tc.want)
			}
		})
	}
}
