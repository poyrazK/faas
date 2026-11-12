package managedpostgres

import (
	"testing"
)

// An empty PR preview database is created on its source's backend, which
// offers the access modes the production bindings use, rather than on a
// region default that may have changed since (ADR-20261102123523).
func TestEmptyPreviewUsesTheSourceBackend(t *testing.T) {
	provider := &fakeProvider{capabilities: testCapabilities()}
	registry := testRegistry(t, provider, func(config *Config) {
		second := config.Backends[0]
		second.ID, second.Namespace = "primary-b", "provider-account-b"
		config.Backends = append(config.Backends, second)
		config.Defaults["us-east-1"] = "primary-b"
	})
	service := testService(t, registry, NewMemoryStore())
	sourceBackend, err := registry.Resolve("primary-a", registry.backends["primary-a"].Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	source := Database{ID: "source", BackendID: sourceBackend.ID, BackendFingerprint: sourceBackend.Fingerprint,
		Spec: Spec{Region: "us-east-1", PostgresMajor: testCapabilities().PostgresMajors[0]}}
	preview, err := service.emptyPreview(source, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if preview.BackendID != "primary-a" || preview.BackendFingerprint != sourceBackend.Fingerprint {
		t.Fatalf("preview backend = %s/%s, want the source's primary-a", preview.BackendID, preview.BackendFingerprint)
	}
	// A source whose backend is gone falls back to the region's default.
	source.BackendID = "retired"
	if preview, err = service.emptyPreview(source, 1<<30); err != nil || preview.BackendID != "primary-b" {
		t.Fatalf("preview of a source on a retired backend = %s, %v; want the default primary-b", preview.BackendID, err)
	}
}
