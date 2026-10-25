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

// fixtureBinary is a fixture server built once per process.
type fixtureBinary struct {
	once  sync.Once
	bytes []byte
	err   error
}

var tracingServer, goTracingHelperServer fixtureBinary

// build compiles the fixture in dir (relative to this package) for the guest,
// or reads the prebuilt binary named by env on hosts without a matching Go
// toolchain.
func (f *fixtureBinary) build(env, name string, dir ...string) ([]byte, error) {
	f.once.Do(func() {
		if prebuilt := os.Getenv(env); prebuilt != "" {
			f.bytes, f.err = os.ReadFile(prebuilt)
			return
		}
		tmp, err := os.MkdirTemp("", "faas-e2e-"+name+"-*")
		if err != nil {
			f.err = err
			return
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		_, thisFile, _, ok := runtime.Caller(0)
		if !ok {
			f.err = fmt.Errorf("%s fixture: cannot locate source", name)
			return
		}
		out := filepath.Join(tmp, name)
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", out, ".")
		cmd.Dir = filepath.Join(append([]string{filepath.Dir(thisFile)}, dir...)...)
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
		if output, err := cmd.CombinedOutput(); err != nil {
			f.err = fmt.Errorf("%s fixture: build: %w\n%s", name, err, output)
			return
		}
		f.bytes, f.err = os.ReadFile(out)
	})
	return f.bytes, f.err
}

// TracingImage is a scratch-style OCI image whose only process is an HTTP
// server with an OpenTelemetry SDK compiled in and no tracing configuration
// of its own (ADR-958). It serves :8080 and /healthz.
func TracingImage(repo string) (fakeImage, string) {
	binary, err := tracingServer.build("FAAS_E2E_TRACING_SERVER_BINARY", "tracing-server", "testdata", "tracingserver")
	if err != nil {
		panic(err)
	}
	return serverImage(repo, "tracing-server", binary)
}

// GoTracingHelperImage is a scratch-style OCI image of an HTTP server
// instrumented only with the Go SDK's tracing package (sdk/go/preview/tracing), which
// lives in the SDK module, so the fixture builds there (ADR-958 amendment).
func GoTracingHelperImage(repo string) (fakeImage, string) {
	binary, err := goTracingHelperServer.build("FAAS_E2E_GO_TRACING_HELPER_BINARY", "go-tracing-helper",
		"..", "..", "sdk", "go", "preview", "tracing", "testdata", "server")
	if err != nil {
		panic(err)
	}
	return serverImage(repo, "go-tracing-helper", binary)
}

// serverImage packs one static server binary at /<name>, serving :8080.
func serverImage(repo, name string, binary []byte) (fakeImage, string) {
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(binary)), Typeflag: tar.TypeReg}); err != nil {
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
			"Cmd": []string{"/" + name}, "Env": []string{}, "WorkingDir": "/",
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
