//go:build linux

package runtimequalification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func hashNativeFile(ctx context.Context, name string) (string, error) {
	//nolint:forbidigo,gosec // Root-protected pinned native host assets; read with a streaming byte bound.
	file, err := os.Open(name)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, readErr := io.Copy(h, io.LimitReader(contextReader{ctx, file}, api.RuntimeQualificationAssetMaxBytes+1))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return "", err
	}
	if n == 0 || n > api.RuntimeQualificationAssetMaxBytes {
		return "", fmt.Errorf("native asset empty or oversized: %w", ErrEvidence)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *linuxNativeSession) snapshotSource(ctx context.Context) error {
	if err := protectNativeGit(s.config.SourceDirectory); err != nil {
		return err
	}
	archiveCtx, cancel := context.WithTimeout(ctx, api.RuntimeQualificationBuildTimeout)
	defer cancel()
	prefix := []string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "core.attributesFile=/dev/null", "-C", s.config.SourceDirectory}
	result, err := nativeCommand(archiveCtx, s.tools["git"], append(append([]string{}, prefix...), "rev-parse", "--verify", s.fixture.SourceCommit+"^{commit}"), "", s.environment("0"))
	if err != nil || result.ExitCode != 0 || strings.TrimSpace(string(result.Stdout)) != s.fixture.SourceCommit {
		return errors.Join(err, fmt.Errorf("native source commit unavailable or differs: %w", ErrEvidence))
	}
	cmd := exec.CommandContext(archiveCtx, s.tools["git"], append(append([]string{}, prefix...), "archive", "--format=tar", s.fixture.SourceCommit)...)
	cmd.Env = s.environment("0")
	cmd.WaitDelay = api.RuntimeQualificationCommandWaitDelay
	configureNativeCommand(cmd)
	stderr := &cappedOutput{limit: api.RuntimeQualificationLogMaxBytes, cancel: cancel}
	cmd.Stderr = stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return errors.Join(err, pipe.Close())
	}
	extractErr := extractNativeSource(archiveCtx, pipe, s.source, api.RuntimeQualificationSourceArchiveMaxBytes)
	if extractErr != nil {
		cancel()
	}
	closeErr := pipe.Close()
	waitErr := cmd.Wait()
	if err := errors.Join(extractErr, closeErr, waitErr); err != nil {
		return fmt.Errorf("snapshot exact native source: %w", err)
	}
	if err := writeEvidence(s.source, ".faas-runtime-qualification-source-sha", []byte(s.fixture.SourceCommit+"\n")); err != nil {
		return err
	}
	goMod, err := os.ReadFile(filepath.Join(s.source, "go.mod"))
	if err != nil {
		return err
	}
	expected := ""
	for _, line := range strings.Split(string(goMod), "\n") {
		if strings.HasPrefix(line, "go ") {
			expected = "go" + strings.TrimSpace(strings.TrimPrefix(line, "go "))
		}
	}
	if expected == "" {
		return fmt.Errorf("pinned source has no Go version: %w", ErrEvidence)
	}
	version, err := nativeCommand(archiveCtx, s.goBinary, []string{"version"}, s.source, s.environment("0"))
	if err != nil || version.ExitCode != 0 || strings.TrimSpace(string(version.Stdout)) != "go version "+expected+" linux/amd64" {
		return errors.Join(err, fmt.Errorf("pinned go version differs from source go.mod: %w", ErrEvidence))
	}
	return nil
}

func (s *linuxNativeSession) Prepare(ctx context.Context, artifacts Artifacts, f Fixture) error {
	prepare, cancel := context.WithTimeout(ctx, api.RuntimeQualificationBuildTimeout)
	defer cancel()
	for _, entry := range []struct{ key, name, digest string }{{f.Target.BaseKey(), "base.ext4", f.Target.BaseSHA256}, {f.LayerKey, "layer.ext4", f.LayerSHA256}} {
		reader, err := artifacts.Get(prepare, entry.key)
		if err != nil {
			return err
		}
		if err := stageNativeAsset(prepare, reader, filepath.Join(s.directory, entry.name), entry.digest, api.RuntimeQualificationAssetMaxBytes); err != nil {
			return err
		}
	}
	kernel, err := protectedNativePath(s.config.KernelPath, false)
	if err != nil {
		return err
	}
	//nolint:forbidigo,gosec // Root-protected pinned native kernel, copied into the private run before boot.
	reader, err := os.Open(kernel)
	if err != nil {
		return err
	}
	if err := stageNativeAsset(prepare, reader, filepath.Join(s.directory, "kernel"), f.KernelSHA256, api.RuntimeQualificationAssetMaxBytes); err != nil {
		return err
	}
	fixtureRaw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if err := writeEvidence(s.directory, "fixture.json", fixtureRaw); err != nil {
		return err
	}
	for _, name := range []string{"vmmd", "vmmd-jail-helper"} {
		result, err := nativeCommand(prepare, s.goBinary, []string{"build", "-o", filepath.Join(s.directory, "bin", name), "./cmd/" + name}, s.source, s.environment("0"))
		persistErr := errors.Join(writeEvidence(s.config.OutputDirectory, name+"-build.stdout", result.Stdout), writeEvidence(s.config.OutputDirectory, name+"-build.stderr", result.Stderr))
		if err != nil || result.ExitCode != 0 || persistErr != nil {
			return errors.Join(err, persistErr, fmt.Errorf("native helper build failed: %w", ErrEvidence))
		}
	}
	result, err := nativeCommand(prepare, s.goBinary, []string{"test", "-tags=metal", "-race", "-c", "-o", filepath.Join(s.directory, "bin", "metal.test"), NativePackage}, s.source, s.environment("1"))
	persistErr := errors.Join(writeEvidence(s.config.OutputDirectory, "metal-build.stdout", result.Stdout), writeEvidence(s.config.OutputDirectory, "metal-build.stderr", result.Stderr))
	if err != nil || result.ExitCode != 0 || persistErr != nil {
		return errors.Join(err, persistErr, fmt.Errorf("native metal test build failed: %w", ErrEvidence))
	}
	return s.Recheck(prepare)
}

func (s *linuxNativeSession) Test(ctx context.Context) (CommandResult, error) {
	environment := append(s.environment("1"),
		"FAAS_RUNTIME_QUALIFICATION_FIXTURE="+filepath.Join(s.directory, "fixture.json"),
		"FAAS_TEST_KERNEL="+filepath.Join(s.directory, "kernel"),
		"FAAS_TEST_BASE_ROOTFS="+filepath.Join(s.directory, "base.ext4"),
		"FAAS_TEST_LAYER_ROOTFS="+filepath.Join(s.directory, "layer.ext4"),
		"FAAS_TEST_VMMD_BINARY="+filepath.Join(s.directory, "bin", "vmmd"),
		"FAAS_TEST_FC_VERSION="+s.config.FirecrackerVersion)
	args := []string{"tool", "test2json", "-t", "-p", NativePackage, filepath.Join(s.directory, "bin", "metal.test"), "-test.v=test2json", "-test.count=1", "-test.run=^" + NativeTest + "$", "-test.timeout=" + api.RuntimeQualificationTestTimeout.String()}
	return nativeCommand(ctx, s.goBinary, args, s.source, environment)
}
