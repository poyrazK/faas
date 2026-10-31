package objectstorage

import (
	"strings"
	"testing"
)

// adr: 20261031122757 — the gateways reach a backend with its gateway
// credential, apid with the bucket-admin one, and a backend that names no
// gateway credential keeps the admin one everywhere.
func TestLoadGatewayUsesGatewayCredential(t *testing.T) {
	split := BackendConfig{ID: "split", Driver: "s3", Region: "us-east-1", S3Region: "us-east-1", Namespace: "project", Endpoint: "https://provider.example.test",
		AccessKeyEnv: "ADMIN_KEY", SecretKeyEnv: "ADMIN_SECRET", GatewayAccessKeyEnv: "GATEWAY_KEY", GatewaySecretKeyEnv: "GATEWAY_SECRET"}
	shared := split
	shared.ID, shared.GatewayAccessKeyEnv, shared.GatewaySecretKeyEnv = "shared", "", ""
	for _, tc := range []struct {
		name         string
		backend      BackendConfig
		set          []string
		admin, gwOK  bool
		gatewayError string
	}{
		{name: "gateway host holds only the gateway credential", backend: split, set: []string{"GATEWAY_KEY", "GATEWAY_SECRET"}, gwOK: true},
		{name: "control plane holds only the admin credential", backend: split, set: []string{"ADMIN_KEY", "ADMIN_SECRET"}, admin: true, gatewayError: "credential environment variables are missing"},
		{name: "no gateway credential keeps the admin one", backend: shared, set: []string{"ADMIN_KEY", "ADMIN_SECRET"}, admin: true, gwOK: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeNodeGateConfig(t, tc.backend)
			getenv := func(name string) string {
				if name == "FAAS_OBJECT_STORAGE_CONFIG" {
					return path
				}
				for _, set := range tc.set {
					if name == set {
						return "synthetic"
					}
				}
				return ""
			}
			if _, err := Load(getenv); (err == nil) != tc.admin {
				t.Fatalf("Load err = %v, want admin credential usable = %v", err, tc.admin)
			}
			registry, err := LoadGateway(getenv)
			if (err == nil) != tc.gwOK || (err != nil && !strings.Contains(err.Error(), tc.gatewayError)) {
				t.Fatalf("LoadGateway err = %v, want gateway credential usable = %v", err, tc.gwOK)
			}
			if err == nil {
				if b, err := registry.Default("us-east-1"); err != nil || b.Fingerprint != fingerprint(tc.backend) {
					t.Fatalf("gateway backend = %+v, %v; the credential must not change its placement fingerprint", b, err)
				}
			}
		})
	}
}

func TestRegistryRejectsPartialGatewayCredential(t *testing.T) {
	base := BackendConfig{ID: "b", Driver: "s3", Region: "us-east-1", S3Region: "us-east-1", Namespace: "project", Endpoint: "https://provider.example.test", AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET"}
	for _, tc := range []struct {
		name   string
		change func(*BackendConfig)
		want   string
	}{
		{name: "key without secret", change: func(b *BackendConfig) { b.GatewayAccessKeyEnv = "GATEWAY_KEY" }, want: "both gateway credential variables"},
		{name: "secret without key", change: func(b *BackendConfig) { b.GatewaySecretKeyEnv = "GATEWAY_SECRET" }, want: "both gateway credential variables"},
		{name: "session token alone", change: func(b *BackendConfig) { b.GatewaySessionTokenEnv = "GATEWAY_TOKEN" }, want: "both gateway credential variables"},
		{name: "gcs backend", change: func(b *BackendConfig) {
			*b = BackendConfig{ID: "b", Driver: "gcs", Region: "us-east-1", Namespace: "project", GCSLocation: "US-EAST1", GCSServiceAccount: "gateway@project.iam.gserviceaccount.com", GatewayAccessKeyEnv: "GATEWAY_KEY", GatewaySecretKeyEnv: "GATEWAY_SECRET"}
		}, want: "mixes GCS and S3 settings"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := base
			tc.change(&b)
			_, err := NewRegistry(Config{DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "b"}, Backends: []BackendConfig{b}}, func(string) string { return "synthetic" }, map[string]Factory{"s3": NewS3, "gcs": NewGCS})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
