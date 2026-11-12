// adr: 20261108160000
package githubd

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// A function a push no longer finds keeps its app, and its check names the
// delete command: no deploy removes a function app (ADR-20261108160000 §2).
func TestPushKeptMessageNamesFunctionDelete(t *testing.T) {
	fn := pushKeptMessage(state.App{Slug: "shop-old", WorkloadName: "old", Type: state.AppTypeFunction})
	for _, want := range []string{"function old is not in gregale.yaml", "gregale app delete shop-old --yes"} {
		if !strings.Contains(fn, want) {
			t.Errorf("function message lacks %q: %s", want, fn)
		}
	}
	if strings.Contains(fn, "Compose") {
		t.Errorf("function message mentions Compose: %s", fn)
	}
	svc := pushKeptMessage(state.App{Slug: "shop-worker", WorkloadName: "worker"})
	if !strings.Contains(svc, "worker is not in the Compose file") || strings.Contains(svc, "app delete") {
		t.Errorf("service message = %s", svc)
	}
}
