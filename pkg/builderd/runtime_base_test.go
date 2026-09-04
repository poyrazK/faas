package builderd

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/imaged"
)

func TestResolveBuildRuntimeBaseRef_UsesMinimalFallback(t *testing.T) {
	env := func(key string) string {
		if key == "FAAS_NODE_NAME" {
			return ""
		}
		return ""
	}
	got, err := resolveBuildRuntimeBaseRef("", FrameworkNode, env)
	if err != nil {
		t.Fatalf("resolveBuildRuntimeBaseRef: %v", err)
	}
	if got != imaged.BaseRefMinimal {
		t.Fatalf("base ref = %q, want %q", got, imaged.BaseRefMinimal)
	}
}

func TestResolveBuildRuntimeBaseRef_UsesNamedNodeDigest(t *testing.T) {
	want := "mirror.example/runner-node24@sha256:" + strings.Repeat("c", 64)
	env := func(key string) string {
		switch key {
		case "FAAS_NODE_NAME":
			return "fsn-2"
		case "FAAS_DEPLOY_BASE_REF_NODE24":
			return want
		default:
			return ""
		}
	}
	got, err := resolveBuildRuntimeBaseRef("node24", FrameworkNode, env)
	if err != nil {
		t.Fatalf("resolveBuildRuntimeBaseRef: %v", err)
	}
	if got != want {
		t.Fatalf("base ref = %q, want %q", got, want)
	}
}

func TestResolveBuildRuntimeBaseRef_RejectsUnpinnedNamedNodeBase(t *testing.T) {
	env := func(key string) string {
		switch key {
		case "FAAS_NODE_NAME":
			return "fsn-2"
		case "FAAS_DEPLOY_BASE_REF_NODE22":
			return "ghcr.io/poyrazk/runner-node22:latest"
		default:
			return ""
		}
	}
	if _, err := resolveBuildRuntimeBaseRef("node22", FrameworkNode, env); err == nil {
		t.Fatal("named node accepted an unpinned runtime base")
	}
}
