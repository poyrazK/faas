package reposcan

import (
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func workloadNamed(t *testing.T, result Result, name string) Workload {
	t.Helper()
	i := slices.IndexFunc(result.Workloads, func(w Workload) bool { return w.Name == name })
	if i < 0 {
		t.Fatalf("no workload %q in %+v", name, result.Workloads)
	}
	return result.Workloads[i]
}

// A1: x-gregale-slug-prefix names new apps <prefix><service> while the
// service names stay short.
func TestComposeSlugPrefixStampsWorkloads(t *testing.T) {
	result, err := scanCompose(t, "x-gregale-slug-prefix: aws-retail-\nservices:\n  ui:\n    image: nginx:1.27\n    ports: [8080]\n  cart:\n    image: nginx:1.27\n    expose: [\"8080\"]\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ui", "cart"} {
		if got := workloadNamed(t, result, name).SlugPrefix; got != "aws-retail-" {
			t.Fatalf("%s SlugPrefix = %q, want aws-retail-", name, got)
		}
	}
}

func TestComposeSlugPrefixRejectsInvalidPrefixesAndNames(t *testing.T) {
	for _, compose := range []string{
		"x-gregale-slug-prefix: Retail-\nservices:\n  ui:\n    image: nginx:1.27\n",
		"x-gregale-slug-prefix: a-very-long-prefix-that-goes-on-\nservices:\n  ui:\n    image: nginx:1.27\n",
		"x-gregale-slug-prefix: shop-\nservices:\n  this-service-name-is-much-too-long-for-apps:\n    image: nginx:1.27\n",
	} {
		if _, err := scanCompose(t, compose); err == nil || !strings.Contains(err.Error(), "x-gregale-slug-prefix") {
			t.Fatalf("scan(%q) error = %v, want an x-gregale-slug-prefix error", compose, err)
		}
	}
}

// A5: a healthcheck's start_period plus retries x interval becomes the
// service's startup deadline; x-gregale-startup-deadline overrides it.
func TestComposeStartupDeadlineFromHealthcheckAndKey(t *testing.T) {
	result, err := scanCompose(t, "services:\n"+
		"  orders:\n    image: nginx:1.27\n    ports: [8080]\n    healthcheck:\n      test: [\"CMD\", \"/bin/check\"]\n      start_period: 30s\n      interval: 10s\n      retries: 3\n"+
		"  quick:\n    image: nginx:1.27\n    ports: [8080]\n    healthcheck:\n      test: [\"CMD\", \"/bin/check\"]\n      start_period: 5s\n      interval: 5s\n      retries: 2\n"+
		"  pinned:\n    image: nginx:1.27\n    ports: [8080]\n    x-gregale-startup-deadline: 90s\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := workloadNamed(t, result, "orders").StartupDeadlineS; got != 60 {
		t.Fatalf("orders StartupDeadlineS = %d, want 60 (30s + 3 x 10s)", got)
	}
	if got := workloadNamed(t, result, "quick").StartupDeadlineS; got != 0 {
		t.Fatalf("quick StartupDeadlineS = %d, want 0 (inside the default window)", got)
	}
	if got := workloadNamed(t, result, "pinned").StartupDeadlineS; got != 90 {
		t.Fatalf("pinned StartupDeadlineS = %d, want 90", got)
	}
	if _, err := scanCompose(t, "services:\n  bad:\n    image: nginx:1.27\n    x-gregale-startup-deadline: 10m\n"); err == nil {
		t.Fatal("a deadline above the absolute cap was accepted")
	}
}

// B2: a curl/wget fetch of the serving port runs from the platform, so the
// image needs no curl; other checks stay commands.
func TestComposeCurlHealthcheckBecomesHTTPReadiness(t *testing.T) {
	result, err := scanCompose(t, "services:\n"+
		"  cart:\n    image: nginx:1.27\n    ports: [\"8080:8080\"]\n    healthcheck:\n      test: [\"CMD-SHELL\", \"curl -f http://localhost:8080/actuator/health || exit 1\"]\n      interval: 10s\n      start_period: 20s\n"+
		"  ui:\n    image: nginx:1.27\n    ports: [8080]\n    healthcheck:\n      test: [\"CMD\", \"wget\", \"-q\", \"-O\", \"/dev/null\", \"http://127.0.0.1:8080/health?deep=1\"]\n"+
		"  other:\n    image: nginx:1.27\n    ports: [8080]\n    healthcheck:\n      test: [\"CMD\", \"curl\", \"-f\", \"http://localhost:9090/health\"]\n"+
		"  posting:\n    image: nginx:1.27\n    ports: [8080]\n    healthcheck:\n      test: [\"CMD\", \"curl\", \"-X\", \"POST\", \"http://localhost:8080/health\"]\n"+
		"  piped:\n    image: nginx:1.27\n    ports: [8080]\n    healthcheck:\n      test: [\"CMD-SHELL\", \"curl -s http://localhost:8080/health | grep ok\"]\n")
	if err != nil {
		t.Fatal(err)
	}
	cart := workloadNamed(t, result, "cart")
	if cart.ReadinessCheck == nil || cart.ReadinessCheck.Path != "/actuator/health" || cart.ReadinessCheck.IntervalS != 10 || cart.ReadinessCheck.StartPeriodS != 20 {
		t.Fatalf("cart readiness = %+v, want /actuator/health every 10 s after 20 s", cart.ReadinessCheck)
	}
	if cart.ImageHealthcheck == nil || !slices.Equal(cart.ImageHealthcheck.Test, []string{"NONE"}) {
		t.Fatalf("cart image healthcheck = %+v, want the command replaced by NONE", cart.ImageHealthcheck)
	}
	if ui := workloadNamed(t, result, "ui"); ui.ReadinessCheck == nil || ui.ReadinessCheck.Path != "/health?deep=1" {
		t.Fatalf("ui readiness = %+v, want /health?deep=1", ui.ReadinessCheck)
	}
	for _, name := range []string{"other", "posting", "piped"} {
		w := workloadNamed(t, result, name)
		if w.ReadinessCheck != nil || w.ImageHealthcheck == nil || w.ImageHealthcheck.Test[0] == "NONE" {
			t.Fatalf("%s converted: readiness %+v, healthcheck %+v", name, w.ReadinessCheck, w.ImageHealthcheck)
		}
	}
}

func TestComposePublicAuthKey(t *testing.T) {
	result, err := scanCompose(t, "x-gregale-public-auth: open\nservices:\n  ui:\n    image: nginx:1.27\n    ports: [8080]\n  admin:\n    image: nginx:1.27\n    ports: [8080]\n    x-gregale-public-auth: bearer\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := workloadNamed(t, result, "ui").PublicAuthMode; got != api.AppPublicAuthModeOpen {
		t.Fatalf("ui PublicAuthMode = %q, want open", got)
	}
	if got := workloadNamed(t, result, "admin").PublicAuthMode; got != api.AppPublicAuthModeBearer {
		t.Fatalf("admin PublicAuthMode = %q, want bearer", got)
	}
	if _, err := scanCompose(t, "services:\n  ui:\n    image: nginx:1.27\n    x-gregale-public-auth: basic\n"); err == nil {
		t.Fatal("x-gregale-public-auth basic was accepted")
	}
}
