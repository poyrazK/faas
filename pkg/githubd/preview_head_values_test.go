package githubd

import (
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

// ADR-20261028090000: a preview imports the plain values of its PR head's
// Compose file, with the project deploy's rules: a secret-looking value is
// skipped, a Docker address of a sibling becomes its Gregale address, and a
// platform-owned binding URL is never overwritten.
func TestPRHeadComposeValuesFollowTheProjectImportRules(t *testing.T) {
	compose := "x-gregale-env-import: true\nservices:\n" +
		"  web:\n    image: ghcr.io/example/web:1\n    depends_on: [billing_api]\n    environment:\n" +
		"      LOG_LEVEL: debug\n      BILLING_URL: http://billing_api:8080/v1\n      STRIPE_KEY: sk_test_123\n" +
		"      GREGALE_SERVICE_BILLING_API_URL: http://elsewhere\n" +
		"  billing_api:\n    image: ghcr.io/example/billing:1\n"
	fsys := fstest.MapFS{"compose.yaml": &fstest.MapFile{Data: []byte(compose)}}
	scan, err := reposcan.Scan(fsys)
	if err != nil {
		t.Fatal(err)
	}
	values := prHeadComposeValues(fsys, scan, api.MustLimitsFor(api.PlanPro))["web"]
	if values["LOG_LEVEL"] != "debug" || values["BILLING_URL"] != "http://billing-api.svc.gregale:10081/v1" {
		t.Fatalf("PR head values = %v; want LOG_LEVEL and the rewritten BILLING_URL", values)
	}
	if _, ok := values["STRIPE_KEY"]; ok {
		t.Fatal("a secret-looking Compose value was imported into a preview")
	}

	bindings := map[string]string{"GREGALE_SERVICE_BILLING_API_URL": "http://billing-api.svc.gregale:10081"}
	merged := withPRHeadValues(bindings, map[string]string{"LOG_LEVEL": "debug", "GREGALE_SERVICE_BILLING_API_URL": "http://elsewhere"})
	if merged["LOG_LEVEL"] != "debug" || merged["GREGALE_SERVICE_BILLING_API_URL"] != "http://billing-api.svc.gregale:10081" {
		t.Fatalf("merged env = %v; want the binding URL kept", merged)
	}

	noImport := fstest.MapFS{"compose.yaml": &fstest.MapFile{Data: []byte("services:\n  web:\n    image: ghcr.io/example/web:1\n    environment:\n      LOG_LEVEL: debug\n")}}
	scan, err = reposcan.Scan(noImport)
	if err != nil {
		t.Fatal(err)
	}
	if got := prHeadComposeValues(noImport, scan, api.MustLimitsFor(api.PlanPro)); len(got) != 0 {
		t.Fatalf("values imported without x-gregale-env-import: %v", got)
	}
}
