package apidserver_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestCreateCustomDomainRejectsUnpublishableChallenge(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	app := apidserver.MustSeedApp(t, e, "dns-name-bounds")
	base := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "."
	for _, domain := range []string{base + strings.Repeat("d", 49), "*." + base + strings.Repeat("d", 49)} {
		response := e.Do(t, http.MethodPost, "/v1/domains", api.CreateCustomDomainRequest{Domain: domain, AppID: app}, nil)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "too long for its DNS verification record") {
			t.Fatalf("domain %q: got %d %s", domain, response.Code, response.Body)
		}
	}
}
