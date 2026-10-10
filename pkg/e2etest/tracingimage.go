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
	tracingServerOnce  sync.Once
	tracingServerBytes []byte
	tracingServerErr   error
)

// tracingServerBinary builds testdata/tracingserver once per process, or reads
// FAAS_E2E_TRACING_SERVER_BINARY on hosts without a matching Go toolchain.
func tracingServerBinary() ([]byte, error) {
	tracingServerOnce.Do(func() {
		if prebuilt := os.Getenv("FAAS_E2E_TRACING_SERVER_BINARY"); prebuilt != "" {
			tracingServerBytes, tracingServerErr = os.ReadFile(prebuilt)
			return
		}
		dir, err := os.MkdirTemp("", "faas-e2e-tracingserver-*")
		if err != nil {
			tracingServerErr = err
			return
		}
		defer func() { _ = os.RemoveAll(dir) }()
		_, thisFile, _, ok := runtime.Caller(0)
		if !ok {
			tracingServerErr = fmt.Errorf("tracing fixture: cannot locate source")
			return
		}
		out := filepath.Join(dir, "tracing-server")
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", out, ".")
		cmd.Dir = filepath.Join(filepath.Dir(thisFile), "testdata", "tracingserver")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
		if output, err := cmd.CombinedOutput(); err != nil {
			tracingServerErr = fmt.Errorf("tracing fixture: build: %w\n%s", err, output)
			return
		}
		tracingServerBytes, tracingServerErr = os.ReadFile(out)
	})
	return tracingServerBytes, tracingServerErr
}

// TracingImage is a scratch-style OCI image whose only process is an HTTP
// server with an OpenTelemetry SDK compiled in and no tracing configuration
// of its own (ADR-958). It serves :8080 and /healthz.
func TracingImage(repo string) (fakeImage, string) {
	binary, err := tracingServerBinary()
	if err != nil {
		panic(err)
	}
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	if err := tw.WriteHeader(&tar.Header{Name: "tracing-server", Mode: 0o755, Size: int64(len(binary)), Typeflag: tar.TypeReg}); err != nil {
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
	config := map[string]any{
		"architecture": "amd64",
		"os":           "linux",
		"config": map[string]any{
			"Cmd": []string{"/tracing-server"}, "Env": []string{}, "WorkingDir": "/",
			"ExposedPorts": map[string]any{"8080/tcp": struct{}{}},
		},
		"rootfs": map[string]any{"type": "layers", "diff_ids": []string{"sha256:" + hex.EncodeToString(diffSum[:])}},
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
