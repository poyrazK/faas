package e2etest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"testing"
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
