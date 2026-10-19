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
