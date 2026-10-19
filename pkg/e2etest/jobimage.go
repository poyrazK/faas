package e2etest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
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
	return nativeFixtureImage(repo, []string{"/job-fixture", "success"})
}

// CustomerWorkflowImage serves real HTTP actions and obtains workload identity
// from guest-init. It shares the native fixture executable with the Job image.
func CustomerWorkflowImage(repo string) (fakeImage, string) {
	return nativeFixtureImage(repo, []string{"/job-fixture", "customer-workflow"})
}

// ProcessJobImage (ADR-1790) is the job fixture with a Docker process config:
// USER 1234 with a passwd entry and home, a WORKDIR that does not exist in
// the image, ENV including a PATH that holds the fixture, and CMD
// `job-fixture whoami` as a bare command.
func ProcessJobImage(repo string) (fakeImage, string) {
	return nativeFixtureImageWith(repo, map[string]any{
		"Cmd":        []string{"job-fixture", "whoami"},
		"Env":        []string{"PATH=/usr/local/bin:/usr/bin:/bin:/", "FIXTURE_FROM_IMAGE=yes"},
		"User":       "1234",
		"WorkingDir": "/srv/app",
	}, map[string]string{
		"etc/passwd": "root:x:0:0:root:/root:/bin/sh\napp:x:1234:1234:app:/home/app:/bin/sh\n",
		"etc/group":  "root:x:0:\napp:x:1234:\n",
	})
}

func nativeFixtureImage(repo string, command []string) (fakeImage, string) {
	return nativeFixtureImageWith(repo, map[string]any{"Cmd": command, "Env": []string{}, "WorkingDir": "/"}, nil)
}

// nativeFixtureImageWith builds the fixture image with the given OCI process
// config and extra root-owned files.
func nativeFixtureImageWith(repo string, process map[string]any, files map[string]string) (fakeImage, string) {
	binary, err := jobFixtureBinary()
	if err != nil {
		panic(err)
	}
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	if err := tw.WriteHeader(&tar.Header{Name: "job-fixture", Mode: 0o755, Size: int64(len(binary)), Typeflag: tar.TypeReg}); err != nil {
		panic(err)
	}
	if _, err := tw.Write(binary); err != nil {
		panic(err)
	}
	dirs := map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		if dir := path.Dir(name); dir != "." && !dirs[dir] {
			dirs[dir] = true
			if err := tw.WriteHeader(&tar.Header{Name: dir + "/", Mode: 0o755, Typeflag: tar.TypeDir}); err != nil {
				panic(err)
			}
		}
		body := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			panic(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			panic(err)
		}
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
		"architecture": runtime.GOARCH,
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
