package api

import (
	"reflect"
	"strings"
	"testing"
)

func TestServiceBindingPolicyEffective(t *testing.T) {
	tests := []struct {
		name string
		in   ServiceBindingPolicy
		want ServiceBindingPolicy
	}{
		{name: "legacy empty defaults to account", want: ServiceBindingPolicyAccount},
		{name: "explicit account", in: ServiceBindingPolicyAccount, want: ServiceBindingPolicyAccount},
		{name: "declared", in: ServiceBindingPolicyDeclared, want: ServiceBindingPolicyDeclared},
		{name: "unknown fails closed", in: ServiceBindingPolicy("future-policy"), want: ServiceBindingPolicyDeclared},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.in.Effective(); got != test.want {
				t.Fatalf("Effective() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestServiceBindingTransportEffectiveAndNormalize(t *testing.T) {
	for _, test := range []struct {
		in   ServiceBindingTransport
		want ServiceBindingTransport
	}{
		{in: "", want: ServiceBindingTransportHTTP},
		{in: ServiceBindingTransportHTTP, want: ServiceBindingTransportHTTP},
		{in: ServiceBindingTransportHTTPS, want: ServiceBindingTransportHTTPS},
		{in: ServiceBindingTransport("future"), want: ServiceBindingTransportHTTPS},
	} {
		if got := test.in.Effective(); got != test.want {
			t.Errorf("%q Effective() = %q, want %q", test.in, got, test.want)
		}
	}
	if got, err := NormalizeServiceBindingTransport(" HTTPS "); err != nil || got != ServiceBindingTransportHTTPS {
		t.Fatalf("normalized transport = %q, %v", got, err)
	}
	if _, err := NormalizeServiceBindingTransport("cleartext"); err == nil {
		t.Fatal("accepted unknown transport")
	}
}

func TestNormalizeAllowedServiceCallers(t *testing.T) {
	got, err := NormalizeAllowedServiceCallers([]string{" Worker ", "frontend", "FRONTEND"})
	if err != nil || !reflect.DeepEqual(got, []string{"frontend", "worker"}) {
		t.Fatalf("normalized callers = %v, %v", got, err)
	}
	empty, err := NormalizeAllowedServiceCallers(nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("explicit empty callers = %v, %v", empty, err)
	}
	for _, name := range []string{"", "../other", "-caller", "caller-", "a.b"} {
		if _, err := NormalizeAllowedServiceCallers([]string{name}); err == nil {
			t.Errorf("accepted invalid caller %q", name)
		}
	}
	names := make([]string, AllowedServiceCallersMax+1)
	for i := range names {
		names[i] = "caller"
	}
	if _, err := NormalizeAllowedServiceCallers(names); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("over-limit error = %v", err)
	}
}

func TestStandaloneServiceBindingProjection(t *testing.T) {
	targets, err := NormalizeServiceBindingTargets([]string{" Identity ", "billing", "BILLING"})
	if err != nil || !reflect.DeepEqual(targets, []string{"billing", "identity"}) {
		t.Fatalf("targets = %v, %v", targets, err)
	}
	bindings := ServiceBindingsForTargets(targets)
	want := []AppServiceBinding{
		{Binding: "GREGALE_SERVICE_BILLING_URL", Service: "billing"},
		{Binding: "GREGALE_SERVICE_IDENTITY_URL", Service: "identity"},
	}
	if !reflect.DeepEqual(bindings, want) {
		t.Fatalf("bindings = %#v, want %#v", bindings, want)
	}
	env := ServiceBindingEnv(map[string]string{
		"CUSTOM":                        "kept",
		"GREGALE_SERVICE_OLD_URL":       "stale",
		"GREGALE_SERVICE_OLD_HTTPS_URL": "stale",
	}, bindings)
	if len(env) != 5 || env["CUSTOM"] != "kept" || env["GREGALE_SERVICE_OLD_URL"] != "" ||
		env["GREGALE_SERVICE_BILLING_URL"] != "http://billing.svc.gregale:10080" ||
		env["GREGALE_SERVICE_BILLING_HTTPS_URL"] != "https://billing.internal" ||
		env["GREGALE_SERVICE_IDENTITY_HTTPS_URL"] != "https://identity.internal" {
		t.Fatalf("env = %#v", env)
	}
	if empty := ServiceBindingEnv(env, nil); !reflect.DeepEqual(empty, map[string]string{"CUSTOM": "kept"}) {
		t.Fatalf("cleared env = %#v", empty)
	}
	for _, invalid := range []string{"", "../billing", "-billing", "billing-"} {
		if _, err := NormalizeServiceBindingTargets([]string{invalid}); err == nil {
			t.Errorf("accepted invalid target %q", invalid)
		}
	}
	over := make([]string, ServiceBindingTargetsMax+1)
	if _, err := NormalizeServiceBindingTargets(over); err == nil {
		t.Fatal("accepted oversized target list")
	}
}

func TestServiceBindingEnvHTTPSFirstKeepsHTTPSAlias(t *testing.T) {
	bindings := []AppServiceBinding{{Binding: ServiceBindingEnvKey("billing"), Service: "billing"}}
	got := ServiceBindingEnvForTransport(map[string]string{
		"GREGALE_SERVICE_OLD_URL":       "stale-http",
		"GREGALE_SERVICE_OLD_HTTPS_URL": "stale-https",
		"CUSTOM":                        "kept",
	}, bindings, ServiceBindingTransportHTTPS)
	want := map[string]string{
		"GREGALE_SERVICE_BILLING_URL":       "https://billing.internal",
		"GREGALE_SERVICE_BILLING_HTTPS_URL": "https://billing.internal",
		"CUSTOM":                            "kept",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("HTTPS-first env = %#v, want %#v", got, want)
	}
}

func TestNormalizeServiceBindingSmokePath(t *testing.T) {
	for _, test := range []struct {
		name string
		in   string
		want string
	}{
		{name: "absolute path", in: "/health", want: "/health"},
		{name: "query retained for request", in: "/health?ready=1", want: "/health?ready=1"},
		{name: "escaped path", in: "/v1/a%2Fb", want: "/v1/a%2Fb"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeServiceBindingSmokePath(test.in)
			if err != nil || got != test.want {
				t.Fatalf("NormalizeServiceBindingSmokePath(%q) = %q, %v; want %q", test.in, got, err, test.want)
			}
		})
	}
	for _, invalid := range []string{"", "health", "//outside.example/path", "https://outside.example/health", "/health#ready", "/bad\\path", "/health\r\nHost: outside.example", strings.Repeat("/", ServiceBindingSmokePathMaxBytes+1)} {
		if _, err := NormalizeServiceBindingSmokePath(invalid); err == nil {
			t.Errorf("accepted invalid smoke path %q", invalid)
		}
	}
}
