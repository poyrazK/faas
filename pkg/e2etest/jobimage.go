package e2etest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

var (
	jobFixtureOnce  sync.Once
	jobFixtureBytes []byte
	jobFixtureErr   error
)

func jobFixtureBinary() ([]byte, error) {
	jobFixtureOnce.Do(func() {
		dir, err := os.MkdirTemp("", "faas-e2e-jobfixture-*")
		if err != nil {
			jobFixtureErr = err
			return
		}
		defer func() { _ = os.RemoveAll(dir) }()
		_, thisFile, _, ok := runtime.Caller(0)
		if !ok {
			jobFixtureErr = fmt.Errorf("job fixture: cannot locate source")
			return
		}
		out := filepath.Join(dir, "job-fixture")
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", out, ".")
		cmd.Dir = filepath.Join(filepath.Dir(thisFile), "testdata", "jobfixture")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
		if output, err := cmd.CombinedOutput(); err != nil {
			jobFixtureErr = fmt.Errorf("job fixture: build: %w\n%s", err, output)
			return
		}
		jobFixtureBytes, jobFixtureErr = os.ReadFile(out)
	})
	return jobFixtureBytes, jobFixtureErr
}

// JobImage is a real scratch-style OCI image containing /job-fixture. Unlike
// HelloImage, it includes an executable that exits, sleeps, or allocates RAM.
func JobImage(repo string) (fakeImage, string) {
	binary, err := jobFixtureBinary()
	if err != nil {
		panic(err)
	}
	return staticFixtureImage(repo, "job-fixture", binary, runtime.GOARCH, []string{"/job-fixture", "success"}, 0)
}

// staticFixtureImage packages a compiled fixture with actual OCI blob and
// uncompressed layer digests. A zero port preserves the jobs image contract.
func staticFixtureImage(repo, filename string, binary []byte, arch string, cmd []string, port int) (fakeImage, string) {
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	if err := tw.WriteHeader(&tar.Header{Name: filename, Mode: 0o755, Size: int64(len(binary)), Typeflag: tar.TypeReg}); err != nil {
		panic(err)
	}
	if _, err := tw.Write(binary); err != nil {
		panic(err)
	}
	if err := tw.Close(); err != nil {
		panic(err)
	}
	var layerBuf bytes.Buffer
	zw := gzip.NewWriter(&layerBuf)
	if _, err := zw.Write(tarBuf.Bytes()); err != nil {
		panic(err)
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	diffSum := sha256.Sum256(tarBuf.Bytes())
	layerSum := sha256.Sum256(layerBuf.Bytes())
	layerDigest := "sha256:" + hex.EncodeToString(layerSum[:])
	process := map[string]any{"Cmd": cmd, "Env": []string{}, "WorkingDir": "/"}
	if port != 0 {
		process["ExposedPorts"] = map[string]any{fmt.Sprintf("%d/tcp", port): struct{}{}}
	}
	config := map[string]any{
		"architecture": arch,
		"os":           "linux",
		"config":       process,
		"rootfs":       map[string]any{"type": "layers", "diff_ids": []string{"sha256:" + hex.EncodeToString(diffSum[:])}},
	}
	configBytes, _ := json.Marshal(config)
	configSum := sha256.Sum256(configBytes)
	configDigest := "sha256:" + hex.EncodeToString(configSum[:])
	manifest := map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config":        map[string]any{"mediaType": "application/vnd.oci.image.config.v1+json", "digest": configDigest, "size": len(configBytes)},
		"layers":        []map[string]any{{"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip", "digest": layerDigest, "size": layerBuf.Len()}},
	}
	manifestBytes, _ := json.Marshal(manifest)
	manifestSum := sha256.Sum256(manifestBytes)
	manifestDigest := "sha256:" + hex.EncodeToString(manifestSum[:])
	return fakeImage{
		configDigest: configDigest, configBytes: configBytes,
		layerBlobs:     []blobEntry{{digest: layerDigest, bytes: layerBuf.Bytes()}},
		manifestDigest: manifestDigest, manifestBytes: manifestBytes,
		manifestMT: "application/vnd.oci.image.manifest.v1+json",
	}, fmt.Sprintf("%s@%s", repo, manifestDigest)
}
