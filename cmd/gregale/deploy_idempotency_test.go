package main

import (
	"strings"
	"testing"
)

func TestDeployIdempotencyKeyIsStableForTheSameIntent(t *testing.T) {
	intent := deployIdempotencyIntent{Slug: "demo", Shape: shapeApp, SourceSHA256: "abc123"}
	first, err := deployIdempotencyKey("", intent)
	if err != nil {
		t.Fatalf("first key: %v", err)
	}
	second, err := deployIdempotencyKey("", intent)
	if err != nil {
		t.Fatalf("second key: %v", err)
	}
	if first != second {
		t.Fatalf("same intent produced different keys: %q vs %q", first, second)
	}
	intent.SourceSHA256 = "different"
	third, err := deployIdempotencyKey("", intent)
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
	key, err := deployIdempotencyKey("  release-42  ", deployIdempotencyIntent{})
	if err != nil {
		t.Fatalf("explicit key: %v", err)
	}
	if key != "release-42" {
		t.Fatalf("explicit key = %q, want release-42", key)
	}
	if _, err := deployIdempotencyKey("bad\nkey", deployIdempotencyIntent{}); err == nil {
		t.Fatal("control character key unexpectedly accepted")
	}
	if _, err := deployIdempotencyKey(strings.Repeat("x", maxDeployIdempotencyKeyLength+1), deployIdempotencyIntent{}); err == nil {
		t.Fatal("overlong key unexpectedly accepted")
	}

	multipart := deployOperationIdempotencyKey(key, "multipart")
	json := deployOperationIdempotencyKey(key, "json")
	if multipart == json || multipart == "" || json == "" {
		t.Fatalf("operation keys not distinct: multipart=%q json=%q", multipart, json)
	}
	if got := deployOperationIdempotencyKey(key, "multipart"); got != multipart {
		t.Fatalf("operation key not deterministic: %q vs %q", multipart, got)
	}
	if len(multipart) > maxDeployIdempotencyKeyLength {
		t.Fatalf("operation key length = %d, want <= %d", len(multipart), maxDeployIdempotencyKeyLength)
	}
}
