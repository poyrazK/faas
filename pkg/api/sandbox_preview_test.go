// adr: 20261027090013 §11 — a preview host has one spelling,
// <port>-<sandbox id>.<domain>, and the preview domain is a DNS name.
package api_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSandboxPreviewHost(t *testing.T) {
	const id = "0b0e6a1c-2f6a-4c8e-9d43-6f3f1d2a9b10"
	h := api.SandboxPreviewHost(3000, id, "preview.example.test")
	if port, got, ok := api.ParseSandboxPreviewHost(h, "preview.example.test"); !ok || port != 3000 || got != id {
		t.Fatalf("parse %q = %d %q %v", h, port, got, ok)
	}
	for _, bad := range []string{
		"3000-" + id + ".other.test", "0-" + id + ".preview.example.test", "65536-" + id + ".preview.example.test",
		"03000-" + id + ".preview.example.test", "3000-" + id[:35] + ".preview.example.test", "x.3000-" + id + ".preview.example.test",
		"3000-" + id[:8] + "x" + id[9:] + ".preview.example.test", "preview.example.test",
	} {
		if _, _, ok := api.ParseSandboxPreviewHost(bad, "preview.example.test"); ok {
			t.Errorf("parsed %q", bad)
		}
	}
	if _, _, ok := api.ParseSandboxPreviewHost(h, ""); ok {
		t.Error("parsed a host with previews off")
	}
}

func TestNormalizeSandboxPreviewDomain(t *testing.T) {
	for raw, want := range map[string]string{"": "", " Preview.Example.Test. ": "preview.example.test", "a.b": "a.b"} {
		if got, err := api.NormalizeSandboxPreviewDomain(raw); err != nil || got != want {
			t.Errorf("%q = %q, %v", raw, got, err)
		}
	}
	for _, bad := range []string{"localhost", "a..b", "-a.b", "a_b.c", "*.preview.test", "https://a.b"} {
		if _, err := api.NormalizeSandboxPreviewDomain(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
