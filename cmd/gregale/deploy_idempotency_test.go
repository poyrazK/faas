package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"strings"
	"testing"
)

func TestDeployIdempotencyKeyIsStableForTheSameIntent(t *testing.T) {
	intent := main.DeployIdempotencyIntent{Slug: "demo", Shape: main.ShapeApp, SourceSHA256: "abc123"}
	first, err := main.DeployIdempotencyKey("", intent)
	if err != nil {
		t.Fatalf("first key: %v", err)
	}
	second, err := main.DeployIdempotencyKey("", intent)
	if err != nil {
		t.Fatalf("second key: %v", err)
	}
	if first != second {
		t.Fatalf("same intent produced different keys: %q vs %q", first, second)
	}
	intent.SourceSHA256 = "different"
	third, err := main.DeployIdempotencyKey("", intent)
	if err != nil {
		t.Fatalf("changed key: %v", err)
	}
	if first == third {
		t.Fatalf("different source digest reused key %q", first)
	}
	if !strings.HasPrefix(first, "gregale-deploy-") {
		t.Fatalf("default key = %q, want gregale-deploy- prefix", first)
	}
}

func TestDeployIdempotencyKeyValidatesExplicitKeysAndScopesOperations(t *testing.T) {
	key, err := main.DeployIdempotencyKey("  release-42  ", main.DeployIdempotencyIntent{})
	if err != nil {
		t.Fatalf("explicit key: %v", err)
	}
	if key != "release-42" {
		t.Fatalf("explicit key = %q, want release-42", key)
	}
	if _, err := main.DeployIdempotencyKey("bad\nkey", main.DeployIdempotencyIntent{}); err == nil {
		t.Fatal("control character key unexpectedly accepted")
	}
	if _, err := main.DeployIdempotencyKey(strings.Repeat("x", main.MaxDeployIdempotencyKeyLength+1), main.DeployIdempotencyIntent{}); err == nil {
		t.Fatal("overlong key unexpectedly accepted")
	}

	multipart := main.DeployOperationIdempotencyKey(key, "multipart")
	json := main.DeployOperationIdempotencyKey(key, "json")
	if multipart == json || multipart == "" || json == "" {
		t.Fatalf("operation keys not distinct: multipart=%q json=%q", multipart, json)
	}
	if got := main.DeployOperationIdempotencyKey(key, "multipart"); got != multipart {
		t.Fatalf("operation key not deterministic: %q vs %q", multipart, got)
	}
	if len(multipart) > main.MaxDeployIdempotencyKeyLength {
		t.Fatalf("operation key length = %d, want <= %d", len(multipart), main.MaxDeployIdempotencyKeyLength)
	}
}

func TestDeployIdempotencyMutableRefsAndSecretBundles(t *testing.T) {
	for _, tc := range []struct {
		name   string
		intent main.DeployIdempotencyIntent
		stable bool
	}{
		{"branch", main.DeployIdempotencyIntent{Repo: "acme/demo", Ref: "main"}, false},
		{"tag", main.DeployIdempotencyIntent{Repo: "acme/demo", Ref: "v1.0"}, false},
		{"short SHA", main.DeployIdempotencyIntent{Repo: "acme/demo", Ref: "abcdef1"}, false},
		{"commit", main.DeployIdempotencyIntent{Repo: "acme/demo", Ref: strings.Repeat("a", 40)}, true},
		{"secrets", main.DeployIdempotencyIntent{SourceSHA256: "abc", HasSecrets: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, err := main.DeployIdempotencyKey("", tc.intent)
			if err != nil {
				t.Fatal(err)
			}
			second, err := main.DeployIdempotencyKey("", tc.intent)
			if err != nil {
				t.Fatal(err)
			}
			if (first == second) != tc.stable {
				t.Fatalf("keys equal=%v, want %v", first == second, tc.stable)
			}
			for i := 0; i < 2; i++ {
				key, err := main.DeployIdempotencyKey("release-42", tc.intent)
				if err != nil || key != "release-42" {
					t.Fatalf("explicit key=%q err=%v", key, err)
				}
			}
		})
	}
}
