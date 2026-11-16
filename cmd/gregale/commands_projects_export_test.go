package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// `project export` writes a Compose file the importer reads back: build roots
// from the live deployments, internal services as expose and public ones as
// ports, env names as ${NAME} references (values stay sealed), and Gregale
// settings as x-gregale-* keys (microservices-demo feedback 2026-11-16).
func TestProjectExportCompose(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/projects/shop":
			_, _ = w.Write([]byte(`{"slug":"shop","exclusions":[],"workloads":[
				{"slug":"shop-web","workload_name":"web","status":"active","depends_on":["cart"]},
				{"slug":"shop-cart","workload_name":"cart","status":"active"}]}`))
		case "/v1/apps/shop-web":
			_, _ = w.Write([]byte(`{"slug":"shop-web","visibility":"public","ram_mb":256,"app_protocol":"http1","manifest":{"port":8080}}`))
		case "/v1/apps/shop-cart":
			_, _ = w.Write([]byte(`{"slug":"shop-cart","visibility":"internal","ram_mb":512,"app_protocol":"grpc","streaming_enabled":true,"manifest":{"ports":[{"port":7070,"protocol":"tcp","internal":true}]}}`))
		case "/v1/apps/shop-web/env":
			_, _ = w.Write([]byte(`{"env":[{"key":"CART_ADDR"},{"key":"DOTNET_EnableDiagnostics"}],"quota_max":100,"count":2}`))
		case "/v1/apps/shop-cart/env":
			_, _ = w.Write([]byte(`{"env":[],"quota_max":100,"count":0}`))
		case "/v1/apps/shop-web/deployments":
			_, _ = w.Write([]byte(`{"items":[{"id":"d2","status":"failed","source_root":"wrong"},{"id":"d1","status":"live","source_root":"src/frontend"}]}`))
		case "/v1/apps/shop-cart/deployments":
			_, _ = w.Write([]byte(`{"items":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")

	out, restore := main.CaptureStdout(t)
	code := main.Run([]string{"projects", "export", "shop"})
	restore()
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.HasPrefix(out.String(), "# Exported from Gregale project \"shop\"") {
		t.Fatalf("missing header:\n%s", out)
	}
	var doc struct {
		Name     string `yaml:"name"`
		Services map[string]struct {
			Build       string            `yaml:"build"`
			Ports       []string          `yaml:"ports"`
			Expose      []string          `yaml:"expose"`
			Environment map[string]string `yaml:"environment"`
			DependsOn   []string          `yaml:"depends_on"`
			Deploy      struct {
				Resources struct {
					Limits struct {
						Memory string `yaml:"memory"`
					} `yaml:"limits"`
				} `yaml:"resources"`
			} `yaml:"deploy"`
			AppProtocol string `yaml:"x-gregale-app-protocol"`
			Streaming   bool   `yaml:"x-gregale-streaming"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("not YAML: %v\n%s", err, out)
	}
	web, cart := doc.Services["web"], doc.Services["cart"]
	if doc.Name != "shop" || web.Build != "./src/frontend" || len(web.Ports) != 1 || web.Ports[0] != "8080:8080" ||
		web.Environment["CART_ADDR"] != "${CART_ADDR}" || web.Environment["DOTNET_EnableDiagnostics"] != "${DOTNET_EnableDiagnostics}" ||
		len(web.DependsOn) != 1 || web.DependsOn[0] != "cart" || web.Deploy.Resources.Limits.Memory != "256M" || web.AppProtocol != "" {
		t.Fatalf("web = %+v\n%s", web, out)
	}
	if cart.Build != "./cart" || len(cart.Expose) != 1 || cart.Expose[0] != "7070" || len(cart.Ports) != 0 ||
		cart.AppProtocol != "grpc" || !cart.Streaming || cart.Deploy.Resources.Limits.Memory != "512M" {
		t.Fatalf("cart = %+v\n%s", cart, out)
	}
}
