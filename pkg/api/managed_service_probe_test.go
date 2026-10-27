package api

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestManagedServiceProbeContract(t *testing.T) {
	spec := ManagedServiceProbeSpec{Version: 1, CallerDeploymentID: uuid.NewString(), TargetAppID: uuid.NewString(), TargetDeploymentID: uuid.NewString(), ReleaseID: uuid.NewString(), Service: "billing", Binding: "CUSTOM_BILLING_URL", Scope: "production", ContextRevision: strings.Repeat("a", 64)}
	parsed, ok := ParseManagedServiceProbeCommand(spec.Command(), false)
	if !ok || parsed != spec || spec.Command()[0] != AppTaskServiceBindingProbeCommand || IsServiceBindingTargetProbeCommand(spec.Command(), false) || IsBindingVerificationCommand(spec.Command(), false) {
		t.Fatalf("managed operation contract: %+v %v", parsed, ok)
	}
	for _, mutate := range []func(*ManagedServiceProbeSpec){
		func(s *ManagedServiceProbeSpec) { s.Version = 2 },
		func(s *ManagedServiceProbeSpec) { s.ReleaseID = "other" },
		func(s *ManagedServiceProbeSpec) { s.ContextRevision = strings.Repeat("A", 64) },
		func(s *ManagedServiceProbeSpec) { s.Binding = "invalid-key" },
		func(s *ManagedServiceProbeSpec) { s.Scope = "../production" },
	} {
		changed := spec
		mutate(&changed)
		if _, ok := ParseManagedServiceProbeCommand(changed.Command(), false); ok {
			t.Fatalf("invalid context accepted: %+v", changed)
		}
	}
	for _, payload := range []string{spec.Command()[2] + "{}", strings.Replace(spec.Command()[2], `"version":1`, `"version":1,"version":1`, 1), strings.Replace(spec.Command()[2], `"version":1`, `"version":1,"extra":true`, 1)} {
		command := spec.Command()
		command[2] = payload
		if _, ok := ParseManagedServiceProbeCommand(command, false); ok {
			t.Fatal("ambiguous or extended context accepted")
		}
	}
	if _, ok := ParseManagedServiceProbeCommand(spec.Command(), true); ok {
		t.Fatal("shell command accepted")
	}
}

// ADR-20261019200059: an HTTP caller's edge has its own contract; HTTPS
// specs keep their original encoding.
func TestManagedServiceProbeHTTPTransport(t *testing.T) {
	spec := ManagedServiceProbeSpec{Version: 1, CallerDeploymentID: uuid.NewString(), TargetAppID: uuid.NewString(), TargetDeploymentID: uuid.NewString(), ReleaseID: uuid.NewString(), Service: "billing", Binding: "GREGALE_SERVICE_BILLING_URL", Scope: "production", ContextRevision: strings.Repeat("a", 64)}
	if strings.Contains(spec.Command()[2], "transport") || spec.Contract() != ManagedServiceProbeContract {
		t.Fatalf("HTTPS spec changed shape: %s %s", spec.Command()[2], spec.Contract())
	}
	plain := spec
	plain.Transport = ServiceBindingTransportHTTP
	if parsed, ok := ParseManagedServiceProbeCommand(plain.Command(), false); !ok || parsed != plain || plain.Contract() != ManagedServiceHTTPProbeContract {
		t.Fatalf("HTTP spec: %+v %v %s", parsed, ok, plain.Contract())
	}
	plain.Transport = ServiceBindingTransportHTTPS
	if plain.Valid() {
		t.Fatal("explicit https transport accepted; HTTPS specs must stay empty")
	}
	item := AppBindingInventoryItem{Type: BindingTypeService, Name: "billing", Binding: spec.Binding, Scope: spec.Scope, ServiceTargetDeploymentID: spec.TargetDeploymentID,
		ServiceContext: &ServiceBindingInventoryContext{Source: "environment_release", CallerDeploymentID: spec.CallerDeploymentID, TargetAppID: spec.TargetAppID,
			ReleaseID: spec.ReleaseID, Revision: spec.ContextRevision, RoutingStatus: "resolved", VerificationSupported: true}}
	for _, tc := range []struct {
		contract, transport string
		want                ServiceBindingTransport
		ok                  bool
	}{
		{ManagedServiceProbeContract, "https", "", true},
		{ManagedServiceHTTPProbeContract, "http", ServiceBindingTransportHTTP, true},
		{ManagedServiceProbeContract, "http", "", false},
		{ManagedServiceHTTPProbeContract, "https", "", false},
	} {
		item.Transport, item.ServiceContext.VerificationContract = tc.transport, tc.contract
		got, ok := ManagedServiceProbeForItem(item)
		if ok != tc.ok || ok && got.Transport != tc.want {
			t.Fatalf("%s/%s: %+v %v", tc.contract, tc.transport, got, ok)
		}
	}
}

func TestServiceBindingProbeReportHTTPTransport(t *testing.T) {
	passed := ServiceBindingProbeCheck{Status: "passed"}
	report := ServiceBindingProbeReport{Service: "billing", Transport: ServiceBindingTransportHTTP, URL: "http://billing.svc.gregale:10081",
		DNS: passed, TLS: ServiceBindingProbeCheck{Status: "not_checked"}, Authorization: passed, Routing: passed}
	if !report.Passed() || report.URL != ServiceBindingCanonicalURL("billing", ServiceBindingTransportHTTP) {
		t.Fatalf("HTTP report did not pass: %+v", report)
	}
	for name, mutate := range map[string]func(*ServiceBindingProbeReport){
		"https url":         func(r *ServiceBindingProbeReport) { r.URL = "https://billing.internal" },
		"tls claimed":       func(r *ServiceBindingProbeReport) { r.TLS.Status = "passed" },
		"unknown transport": func(r *ServiceBindingProbeReport) { r.Transport = "h2c" },
		"https managed spec": func(r *ServiceBindingProbeReport) {
			r.ManagedService = &ManagedServiceProbeSpec{Version: 1, CallerDeploymentID: uuid.NewString(), TargetAppID: uuid.NewString(), TargetDeploymentID: uuid.NewString(),
				ReleaseID: uuid.NewString(), Service: "billing", Binding: "GREGALE_SERVICE_BILLING_URL", Scope: "production", ContextRevision: strings.Repeat("a", 64)}
			r.TargetDeploymentID, r.HTTPStatus = r.ManagedService.TargetDeploymentID, 204
		},
	} {
		changed := report
		mutate(&changed)
		if changed.Passed() {
			t.Fatalf("%s: report passed", name)
		}
	}
}
