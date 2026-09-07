package main

import (
	"runtime"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/sched"
)

func TestConfiguredRuntimeBaseGenerations(t *testing.T) {
	digest := strings.Repeat("a", 64)
	env := map[string]string{
		"FAAS_NODE_NAME":                     "fsn-test.faas",
		"FAAS_DEPLOY_BASE_REF_MINIMAL":       "ghcr.io/example/minimal@sha256:" + digest,
		"FAAS_DEPLOY_BASE_REF_NODE22":        "ghcr.io/example/node22@sha256:" + digest,
		"FAAS_DEPLOY_BASE_REF_PYTHON312":     "ghcr.io/example/python312@sha256:" + digest,
		"FAAS_DEPLOY_BASE_REF_GO124":         "ghcr.io/example/go124@sha256:" + digest,
		"FAAS_DEPLOY_BASE_REF_GO124_ALPINE":  "ghcr.io/example/go124-alpine@sha256:" + digest,
		"FAAS_DEPLOY_BASE_REF_NODE24":        "ghcr.io/example/node24@sha256:" + digest,
		"FAAS_DEPLOY_BASE_REF_PYTHON313":     "ghcr.io/example/python313@sha256:" + digest,
		"FAAS_DEPLOY_BASE_REF_DEBIAN_PARENT": "ghcr.io/example/parent@sha256:" + digest,
	}
	got, err := configuredRuntimeBaseGenerations(func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("configuredRuntimeBaseGenerations: %v", err)
	}
	if len(got) != 7 {
		t.Fatalf("generation count = %d, want 7", len(got))
	}
	key := sched.BaseKeyForArch(imaged.RuntimeGo124, runtime.GOARCH)
	if got[key] != env["FAAS_DEPLOY_BASE_REF_GO124"] {
		t.Errorf("go124 generation = %q, want %q", got[key], env["FAAS_DEPLOY_BASE_REF_GO124"])
	}
}

func TestConfiguredRuntimeBaseGenerations_NamedNodeRequiresPinnedRefs(t *testing.T) {
	_, err := configuredRuntimeBaseGenerations(func(key string) string {
		if key == "FAAS_NODE_NAME" {
			return "fsn-test.faas"
		}
		return ""
	})
	if err == nil {
		t.Fatal("expected missing digest-pinned runtime refs to fail")
	}
}
