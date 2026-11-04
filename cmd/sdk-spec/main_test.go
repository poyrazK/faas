// adr: 20261023115741 — the SDKs are generated from the GA and beta spec.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestSDKSpecInSync(t *testing.T) {
	want, err := render()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../sdk/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("sdk/openapi.yaml is stale; run 'make sdk-spec' and regenerate the SDKs ('make sdk-gen-node sdk-gen-python')")
	}
}

func TestSDKSpecKeepsGAAndDropsPreviewAndOperatorPaths(t *testing.T) {
	spec, err := render()
	if err != nil {
		t.Fatal(err)
	}
	text := string(spec)
	for _, path := range []string{"\n  /v1/apps:", "\n  /v1/apps/{slug}/env:", "\n  /v1/crons:"} {
		if !strings.Contains(text, path) {
			t.Errorf("GA path %q missing from the SDK spec", strings.TrimSpace(path))
		}
	}
	for _, prefix := range []string{"\n  /v1/realtime/", "\n  /v1/admin/", "\n  /v1/service-map"} {
		if strings.Contains(text, prefix) {
			t.Errorf("preview, internal or operator path %q is in the SDK spec", strings.TrimSpace(prefix))
		}
	}
}
