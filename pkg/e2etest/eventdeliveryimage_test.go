package e2etest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"debug/elf"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/onebox-faas/faas/pkg/oci"
)

// The native gate must ship the Linux/amd64 consumer even when assembled on
// another architecture, and its layer/config digests must describe that file.
func TestEventDeliveryConsumerImage(t *testing.T) {
	image, _, err := EventDeliveryConsumerImage(t.Context(), "library/event-consumer")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		OS, Architecture string
		Config           struct {
			Cmd, Env     []string
			ExposedPorts map[string]struct{}
		}
	}
	if err := json.Unmarshal(image.configBytes, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.OS != "linux" || cfg.Architecture != "amd64" || len(cfg.Config.Cmd) != 1 || cfg.Config.Cmd[0] != "/event-consumer" {
		t.Fatalf("native consumer process contract: %+v", cfg)
	}
	if _, ok := cfg.Config.ExposedPorts["8080/tcp"]; !ok || len(cfg.Config.Env) != 0 {
		t.Fatalf("native consumer port or environment contract: %+v", cfg.Config)
	}
	if len(image.layerBlobs) != 1 {
		t.Fatalf("native consumer has %d layers, want one static executable", len(image.layerBlobs))
	}
	zr, err := gzip.NewReader(bytes.NewReader(image.layerBlobs[0].bytes))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	tr := tar.NewReader(zr)
	header, err := tr.Next()
	if err != nil || header.Name != "event-consumer" || header.Mode != 0o755 {
		t.Fatalf("native consumer executable header=%+v err=%v", header, err)
	}
	binary, err := io.ReadAll(tr)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := elf.NewFile(bytes.NewReader(binary))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = executable.Close() }()
	if executable.Machine != elf.EM_X86_64 {
		t.Fatalf("native consumer ELF machine=%s", executable.Machine)
	}
	for _, program := range executable.Progs {
		if program.Type == elf.PT_INTERP {
			t.Fatal("native consumer requires a dynamic loader absent from its scratch image")
		}
	}
	if _, err := tr.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("native consumer image contains unexpected additional files: %v", err)
	}
	registry := NewFakeRegistry()
	defer registry.Close()
	ref := registry.AddImage("library/event-consumer", image)
	// Read through the OCI client to verify content hashes, config and layers
	// rather than accepting matching labels in the in-memory fixture.
	client := oci.NewRegistryClient(oci.WithHTTPClient(registry.srv.Client()), oci.WithEndpoint("http", registry.Host()))
	manifest, err := client.PullManifest(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range append([]oci.Descriptor{manifest.Config}, manifest.Layers...) {
		reader, err := client.PullBlob(t.Context(), "library/event-consumer", descriptor.Digest)
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := io.Copy(io.Discard, reader)
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("native consumer OCI content verification: read=%v close=%v", readErr, closeErr)
		}
	}
}
