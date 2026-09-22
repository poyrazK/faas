package api

import "testing"

func TestCreateDeploymentRequestNormalizeCompanions(t *testing.T) {
	req := &CreateDeploymentRequest{Companions: Companions{{Name: "proxy"}}}
	if p := req.NormalizeCompanions(); p != nil {
		t.Fatalf("NormalizeCompanions: %+v", p)
	}
	if len(req.Companions) != 0 || len(req.Sidecars) != 1 || req.Sidecars[0].Name != "proxy" {
		t.Fatalf("request = %+v", req)
	}

	conflict := &CreateDeploymentRequest{
		Companions: Companions{{Name: "new"}},
		Sidecars:   Sidecars{{Name: "old"}},
	}
	if p := conflict.NormalizeCompanions(); p == nil || p.Status != 400 {
		t.Fatalf("conflict problem = %+v, want 400", p)
	}
}

func TestCompanionPresetAndPrimaryIngressValidation(t *testing.T) {
	limits := MustLimitsFor(PlanFree)
	managed := Sidecar{Name: "otel", Preset: "opentelemetry", Type: SidecarTypeSidecar}
	if p := managed.Validate(limits); p != nil {
		t.Fatalf("preset-only companion: %+v", p)
	}

	badIngress := Sidecar{Name: "proxy", Image: "registry.example.com/proxy@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Type: SidecarTypeSidecar, PrimaryIngress: true}
	if p := badIngress.Validate(limits); p == nil {
		t.Fatal("primary ingress without port was accepted")
	}
	badIngress.Port = 8081
	if p := badIngress.Validate(limits); p != nil {
		t.Fatalf("primary ingress companion: %+v", p)
	}
}
