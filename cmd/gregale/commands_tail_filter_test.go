package main

import (
	"bytes"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// /v1/events frames carry app_id but no app_slug. `gregale tail --app
// e2e-probe` compared the slug against both fields and dropped every frame
// (production: nothing printed for 75 s while the app's cron completed).
func TestWriteTailFrameFiltersByResolvedAppID(t *testing.T) {
	var buf bytes.Buffer
	prevStdout, prevJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &buf, false
	t.Cleanup(func() { osStdout, jsonOutput = prevStdout, prevJSON })

	filter := tailFilter{appID: "app-1", slugs: map[string]string{"app-1": "e2e-probe", "app-2": "other"}}
	for _, data := range []string{
		`{"state":"completed","app_id":"app-2","source":"cron","invocation_id":"inv-2"}`,
		`{"state":"completed","app_id":"app-1","source":"cron","invocation_id":"inv-1"}`,
	} {
		if err := writeTailFrame(api.Event{Event: "invocation_done", Data: data}, filter); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := buf.String(), "inv-1 e2e-probe completed\n"; got != want {
		t.Fatalf("tail output = %q, want %q", got, want)
	}

	buf.Reset()
	all := tailFilter{slugs: filter.slugs}
	if err := writeTailFrame(api.Event{Event: "invocation_done", Data: `{"state":"failed","app_id":"app-3","invocation_id":"inv-3"}`}, all); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "inv-3 app-3 failed\n"; got != want {
		t.Fatalf("unknown app label = %q, want the app id", got)
	}
}
