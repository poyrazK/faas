package e2etest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/onebox-faas/faas/pkg/oci"
)

// layerEntries lists the tar entries of a gzip layer blob by name -> mode.
func layerEntries(t *testing.T, blob []byte) map[string]int64 {
	t.Helper()
	zr, err := gzip.NewReader(bytesReader(blob))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	out := map[string]int64{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = h.Mode
	}
	return out
}

func imageCmd(t *testing.T, img fakeImage) []string {
	t.Helper()
	var cfg struct {
		Config struct{ Cmd []string } `json:"config"`
	}
	if err := json.Unmarshal(img.configBytes, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg.Config.Cmd
}

// The image must be a self-contained app: the server binary plus the body,
// and a Cmd that runs the server. Anything relying on a shell in the image
// is the fixture that could never run on a real base.
func TestHelloImage_IsASelfContainedApp(t *testing.T) {
	img, _ := HelloImage("library/hello", "hello from faas")
	if got := imageCmd(t, img); len(got) != 1 || got[0] != "/hello-server" {
		t.Fatalf("Cmd = %v, want [/hello-server]; a shell command needs a /bin/sh the image does not ship", got)
	}
	for _, blob := range img.layerBlobs {
		entries := layerEntries(t, blob.bytes)
		if mode, ok := entries["hello-server"]; !ok || mode&0o111 == 0 {
			t.Errorf("layer lacks an executable hello-server (entries=%v)", entries)
		}
		if _, ok := entries["app/hello.txt"]; !ok {
			t.Errorf("layer lacks app/hello.txt (entries=%v)", entries)
		}
	}
}

// Every variant runs the same binary; the shape differs only by flags.
func TestImageVariants_RunHelloServer(t *testing.T) {
	cases := map[string]struct {
		img  fakeImage
		want []string
	}{}
	i1, _ := HelloImageAboveBase("library/hello", "x")
	i2, _ := CPUBoundImage("library/hot")
	i3, _ := WedgedLoopImage("library/wedged")
	i4, _ := HelloImageWithoutHealthz("library/tcp", "x")
	cases["above-base"] = struct {
		img  fakeImage
		want []string
	}{i1, []string{"/hello-server"}}
	cases["cpu-bound"] = struct {
		img  fakeImage
		want []string
	}{i2, []string{"/hello-server", "-spin"}}
	cases["wedged"] = struct {
		img  fakeImage
		want []string
	}{i3, []string{"/hello-server", "-spin", "-ignore-term", "-no-listen"}}
	cases["tcp-readiness"] = struct {
		img  fakeImage
		want []string
	}{i4, []string{"/hello-server", "-no-healthz"}}
	for name, c := range cases {
		got := imageCmd(t, c.img)
		if len(got) != len(c.want) {
			t.Errorf("%s: Cmd = %v, want %v", name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: Cmd = %v, want %v", name, got, c.want)
				break
			}
		}
	}
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

func TestHelloImageProcessContractResolves(t *testing.T) {
	registry := NewFakeRegistry()
	t.Cleanup(registry.Close)
	img, _ := HelloImageWithProcessContract("library/process-contract", "hello", "1001:1001")
	ref := registry.AddImage("library/process-contract", img)
	client := oci.NewRegistryClient(oci.WithEndpoint("http", registry.Host()))
	resolved, err := client.ResolveImage(context.Background(), ref, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := resolved.Config
	if cfg.User != "1001:1001" || cfg.WorkingDir != "/app" || cfg.Env["FIXTURE_MARKER"] != "container-contract" {
		t.Fatalf("incorrect fixture process config: %+v", cfg)
	}
	if len(cfg.Entrypoint) != 1 || cfg.Entrypoint[0] != "/hello-server" || len(cfg.Cmd) != 1 || cfg.Cmd[0] != "-contract" {
		t.Fatalf("incorrect fixture command: entrypoint=%v cmd=%v", cfg.Entrypoint, cfg.Cmd)
	}
	if resolved.Digest != img.manifestDigest {
		t.Fatal("fixture digest chain changed")
	}
}

func TestPortableHelloImageRetainsBodyAndForeignBase(t *testing.T) {
	img, _ := HelloImageWithoutHealthz("library/portable", "portable-body")
	var cfg struct {
		RootFS struct {
			DiffIDs []string `json:"diff_ids"`
		} `json:"rootfs"`
	}
	if err := json.Unmarshal(img.configBytes, &cfg); err != nil {
		t.Fatal(err)
	}
	foreign, _ := BaseLayerImage("library/foreign", "foreign base")
	if _, err := oci.LayersAboveBase([]string{fixtureLayerDiffID(foreign.layerBlobs[0].bytes)}, cfg.RootFS.DiffIDs); !errors.Is(err, oci.ErrLayersNotAboveBase) {
		t.Fatalf("portable fixture does not force full-rootfs: %v", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(img.layerBlobs[0].bytes))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			t.Fatal("fixture body missing")
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == "app/hello.txt" {
			body, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != "portable-body" {
				t.Fatalf("fixture body=%q", body)
			}
			return
		}
	}
}

func TestHelloImageProcessHealthcheckResolves(t *testing.T) {
	registry := NewFakeRegistry()
	t.Cleanup(registry.Close)
	image, _ := HelloImageWithProcessHealthcheck("library/process-health", "hello", "1001:2001")
	ref := registry.AddImage("library/process-health", image)
	client := oci.NewRegistryClient(oci.WithEndpoint("http", registry.Host()))
	resolved, err := client.ResolveImage(context.Background(), ref, nil)
	if err != nil {
		t.Fatal(err)
	}
	check := resolved.Config.Healthcheck
	if check == nil || len(check.Test) != 3 || check.Test[0] != "CMD" || check.Test[1] != "/hello-server" || check.Test[2] != "-probe-contract" || check.IntervalS != 1 || check.TimeoutS != 3 || check.Retries != 3 {
		t.Fatalf("incorrect image exec-probe contract: %+v", check)
	}
	if resolved.Config.User != "1001:2001" || resolved.Digest != image.manifestDigest {
		t.Fatalf("identity or digest changed: %+v", resolved)
	}
}
