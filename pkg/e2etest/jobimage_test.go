package e2etest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"testing"
)

func TestJobImageContainsExecutableCommand(t *testing.T) {
	img, _ := JobImage("test/job")
	if len(img.layerBlobs) != 1 {
		t.Fatalf("layers = %d, want 1", len(img.layerBlobs))
	}
	zr, err := gzip.NewReader(bytes.NewReader(img.layerBlobs[0].bytes))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	hdr, err := tr.Next()
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Name != "job-fixture" || hdr.Mode&0o111 == 0 {
		t.Fatalf("job image entry = %+v", hdr)
	}
	magic := make([]byte, 4)
	if _, err := io.ReadFull(tr, magic); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(magic, []byte{0x7f, 'E', 'L', 'F'}) {
		t.Fatalf("fixture is not an ELF executable: %x", magic)
	}
	var config struct {
		Config struct {
			Cmd []string `json:"Cmd"`
		} `json:"config"`
	}
	if err := json.Unmarshal(img.configBytes, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Config.Cmd) != 2 || config.Config.Cmd[0] != "/job-fixture" {
		t.Fatalf("image command = %v", config.Config.Cmd)
	}
}
