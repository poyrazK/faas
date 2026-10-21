// Whitebox tests for the preview CLI surface (issue #961
// Mega-C PR-1, leaf 3).
//
// Coverage:
//   - cmdPreviewDestroy hits POST /v1/preview/{slug}/destroy
//   - cmdPreview dispatcher routes "destroy" to the handler
//   - missing-arg branch exits 1 with usage
//   - 404 preview_not_found surfaces as exit 1 (NOT exit 0)
//
// Pins the route + HTTP method at the CLI surface. The
// wire-shape gate is covered by the SDK round-trip tests in
// pkg/api/client_method_sweep2_test.go.

package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"net/http"
	"strings"
	"testing"
)

func TestPreviewDestroy_HappyPath(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, "", http.StatusNoContent)
	if code := main.CmdPreviewDestroy([]string{"pr-42-acme"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if (*f.SawMethod()) != "POST" || (*f.SawPath()) != "/v1/preview/pr-42-acme/destroy" {
		t.Errorf("route = %s %s, want POST /v1/preview/pr-42-acme/destroy", (*f.SawMethod()), (*f.SawPath()))
	}
}

func TestPreviewDestroy_MissingArgExitsOne(t *testing.T) {
	main.ResetJSONOut(t)
	// Even with no API call (we never hit the server), the
	// arg-count guard must exit 1 with the usage line.
	if code := main.CmdPreviewDestroy(nil); code != 1 {
		t.Fatalf("nil args: exit = %d, want 1", code)
	}
	if code := main.CmdPreviewDestroy([]string{}); code != 1 {
		t.Fatalf("empty args: exit = %d, want 1", code)
	}
	if code := main.CmdPreviewDestroy([]string{"a", "b"}); code != 1 {
		t.Fatalf("two args: exit = %d, want 1 (the command takes exactly one slug)", code)
	}
}

func TestPreviewDestroy_ProductionAppReturnsNonZero(t *testing.T) {
	main.ResetJSONOut(t)
	main.AuthedFakeAPI(t,
		`{"code":"preview_not_found","title":"Preview not found","detail":"the slug does not identify a preview app; use DELETE /v1/apps/{slug} to destroy a production app","status":404}`,
		http.StatusNotFound,
	)
	if code := main.CmdPreviewDestroy([]string{"prod-app"}); code == 0 {
		t.Errorf("exit = 0, want non-zero (production app on preview route must surface as an error, not a silent success)")
	}
}

// TestPreviewDispatch_DestroyRegistered: the cmdPreview
// dispatcher must route "destroy" to the new handler. Without
// this guard, a typo like "case 'destory'" would silently fall
// through to the unknown-subcommand branch.
func TestPreviewDispatch_DestroyRegistered(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, "", http.StatusNoContent)
	if code := main.CmdPreview([]string{"destroy", "pr-7-foo"}); code != 0 {
		t.Fatalf("cmdPreview destroy exit = %d, want 0", code)
	}
	if !strings.HasSuffix((*f.SawPath()), "/v1/preview/pr-7-foo/destroy") {
		t.Errorf("dispatch path = %s, want it to end with /v1/preview/pr-7-foo/destroy", (*f.SawPath()))
	}
}

func TestPreviewDispatch_UnknownSubcommandExitsOne(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.CmdPreview([]string{"rebuild"}); code != 1 {
		t.Errorf("unknown subcommand: exit = %d, want 1", code)
	}
}

func TestPreviewDispatch_NoArgsListsPreviews(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `[]`, http.StatusOK)
	if code := main.CmdPreview(nil); code != 0 {
		t.Errorf("no args: exit = %d, want 0", code)
	}
	if (*f.SawMethod()) != http.MethodGet || (*f.SawPath()) != "/v1/apps" {
		t.Errorf("request = %s %s, want GET /v1/apps", (*f.SawMethod()), (*f.SawPath()))
	}
}
