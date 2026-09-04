// commands_artifact.go — release artifact publication and verification.
//
// Multi-box compute nodes use the OCI storage backend for every shared
// artifact. The release bundle carries the release-pinned Firecracker kernel,
// but carrying a file in the bundle is not enough: vmmd resolves the
// release-pinned kernel through StorageBackend at wake time. These commands
// make the release pipeline publish that exact key and make node adoption
// fail before a node can become schedulable when the key is absent.

package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/manifest"
	"github.com/onebox-faas/faas/pkg/storage"
)

const dispatchArtifact = "artifact"

var storageEnvNames = []string{
	"FAAS_STORAGE_BACKEND",
	"FAAS_STORAGE_ROOT",
	"FAAS_APPS_ROOT",
	"FAAS_STORAGE_LOCAL_PREFIXES",
	"FAAS_STORAGE_CACHE_DIR",
	"FAAS_STORAGE_CACHE_MAX_BYTES",
	"FAAS_STORAGE_CACHE_SERVE_STALE",
	"FAAS_OCI_REGISTRY",
	"FAAS_OCI_REPO_PREFIX",
	"FAAS_OCI_USERNAME",
	"FAAS_OCI_PASSWORD",
	"FAAS_OCI_TIMEOUT_SECONDS",
	"FAAS_REQUIRE_SHARED_ARTIFACTS",
}

var storageEnvName = regexp.MustCompile(`^FAAS_(?:STORAGE_[A-Z0-9_]+|APPS_[A-Z0-9_]+|OCI_[A-Z0-9_]+|REQUIRE_SHARED_ARTIFACTS)$`)

type artifactReport struct {
	Operation      string `json:"operation"`
	Key            string `json:"key"`
	Backend        string `json:"backend"`
	Bytes          int64  `json:"bytes"`
	SHA256         string `json:"sha256"`
	AlreadyPresent bool   `json:"already_present,omitempty"`
}

// cmdArtifactDispatch exposes the small operator surface used by release
// automation and node_join. It intentionally does not require FAAS_PG_DSN or
// an API token: publishing a shared blob is a registry operation.
func cmdArtifactDispatch(args []string) int {
	if len(args) == 0 {
		printArtifactUsage(os.Stderr)
		return 1
	}
	switch args[0] {
	case "publish":
		return cmdArtifactPublish(args[1:])
	case "verify":
		return cmdArtifactVerify(args[1:])
	case flagHelpShort, flagHelpLong:
		printArtifactUsage(os.Stderr)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "gregalectl artifact: unknown subcommand %q (expected: publish, verify)\n", args[0])
		return 1
	}
}

func printArtifactUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, `usage: gregalectl artifact <publish|verify> --env-file PATH --manifest-file PATH [flags]

Publish copies the release-pinned kernel from --file into the shared storage
backend at kernel/<release.firecracker_version>. Existing content is accepted
only when its digest matches release.kernel_digest; it is never overwritten.

Flags:
  --env-file PATH       storage.env containing the shared storage contract (required)
  --manifest-file PATH  signed production manifest (required)
  --file PATH           release vmlinux file (publish only)
  --no-cache            bypass the local read-through cache
  --refresh              fetch from shared storage and replace the local cache (verify only)
  --json                emit a machine-readable report

Examples:
  gregalectl artifact publish --env-file=/etc/faas/storage.env --manifest-file=/etc/faas/manifest.yaml --file=/var/lib/faas/bootstrap/vmlinux
  gregalectl artifact verify --env-file=/etc/faas/storage.env --manifest-file=/etc/faas/manifest.yaml`)
}

type artifactOptions struct {
	envFile      string
	manifestFile string
	file         string
	noCache      bool
	refresh      bool
}

func parseArtifactFlags(args []string, operation string) (artifactOptions, int, bool) {
	fs := flag.NewFlagSet("gregalectl artifact "+operation, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var opts artifactOptions
	fs.StringVar(&opts.envFile, "env-file", "", "storage.env path (required)")
	fs.StringVar(&opts.manifestFile, "manifest-file", "", "production manifest path (required)")
	fs.StringVar(&opts.file, "file", "", "artifact file (required for publish)")
	fs.BoolVar(&opts.noCache, "no-cache", false, "bypass the local read-through cache")
	fs.BoolVar(&opts.refresh, "refresh", false, "fetch from shared storage and replace the local cache (verify only)")
	if err := fs.Parse(args); err != nil {
		return artifactOptions{}, 2, false
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "gregalectl artifact %s: unexpected positional argument %q\n", operation, fs.Arg(0))
		return artifactOptions{}, 2, false
	}
	if opts.envFile == "" || opts.manifestFile == "" {
		fmt.Fprintf(os.Stderr, "gregalectl artifact %s: --env-file and --manifest-file are required\n", operation)
		return artifactOptions{}, 2, false
	}
	if operation == "publish" && opts.file == "" {
		fmt.Fprintln(os.Stderr, "gregalectl artifact publish: --file is required")
		return artifactOptions{}, 2, false
	}
	if operation == "verify" && opts.file != "" {
		fmt.Fprintln(os.Stderr, "gregalectl artifact verify: --file is not supported")
		return artifactOptions{}, 2, false
	}
	if operation == "publish" && opts.refresh {
		fmt.Fprintln(os.Stderr, "gregalectl artifact publish: --refresh is only supported by verify")
		return artifactOptions{}, 2, false
	}
	return opts, 0, true
}

func cmdArtifactPublish(args []string) int {
	opts, code, ok := parseArtifactFlags(args, "publish")
	if !ok {
		return code
	}
	contract, err := loadArtifactContract(opts.manifestFile)
	if err != nil {
		return printErr("gregalectl artifact publish", err)
	}
	cleanup, err := loadStorageEnv(opts.envFile)
	if err != nil {
		return printErr("gregalectl artifact publish", err)
	}
	defer cleanup()
	if err := validateArtifactStorageContract(); err != nil {
		return printErr("gregalectl artifact publish", err)
	}
	if opts.noCache {
		if err := os.Setenv("FAAS_STORAGE_CACHE_DIR", ""); err != nil {
			return printErr("gregalectl artifact publish", err)
		}
	}
	be, err := storage.BackendFromEnv()
	if err != nil {
		return printErr("gregalectl artifact publish", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	report, err := publishArtifact(ctx, be, contract.key, contract.digest, opts.file)
	if err != nil {
		return printErr("gregalectl artifact publish", err)
	}
	report.Backend = os.Getenv("FAAS_STORAGE_BACKEND")
	if jsonEnabled() {
		jsonEmit(os.Stdout, report)
	} else {
		fmt.Printf("artifact publish: key=%s sha256=%s bytes=%d already_present=%t\n", report.Key, report.SHA256, report.Bytes, report.AlreadyPresent)
	}
	return 0
}

func cmdArtifactVerify(args []string) int {
	opts, code, ok := parseArtifactFlags(args, "verify")
	if !ok {
		return code
	}
	contract, err := loadArtifactContract(opts.manifestFile)
	if err != nil {
		return printErr("gregalectl artifact verify", err)
	}
	cleanup, err := loadStorageEnv(opts.envFile)
	if err != nil {
		return printErr("gregalectl artifact verify", err)
	}
	defer cleanup()
	if err := validateArtifactStorageContract(); err != nil {
		return printErr("gregalectl artifact verify", err)
	}
	if opts.noCache {
		if err := os.Setenv("FAAS_STORAGE_CACHE_DIR", ""); err != nil {
			return printErr("gregalectl artifact verify", err)
		}
	}
	if opts.refresh {
		previous, wasSet := os.LookupEnv("FAAS_STORAGE_CACHE_REFRESH")
		if err := os.Setenv("FAAS_STORAGE_CACHE_REFRESH", "true"); err != nil {
			return printErr("gregalectl artifact verify", err)
		}
		defer func() {
			if wasSet {
				_ = os.Setenv("FAAS_STORAGE_CACHE_REFRESH", previous)
			} else {
				_ = os.Unsetenv("FAAS_STORAGE_CACHE_REFRESH")
			}
		}()
	}
	be, err := storage.BackendFromEnv()
	if err != nil {
		return printErr("gregalectl artifact verify", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	report, err := verifyArtifact(ctx, be, contract.key, contract.digest)
	if err != nil {
		return printErr("gregalectl artifact verify", err)
	}
	report.Backend = os.Getenv("FAAS_STORAGE_BACKEND")
	if jsonEnabled() {
		jsonEmit(os.Stdout, report)
	} else {
		fmt.Printf("artifact verify: key=%s sha256=%s bytes=%d\n", report.Key, report.SHA256, report.Bytes)
	}
	return 0
}

type artifactContract struct {
	key    string
	digest string
}

func loadArtifactContract(path string) (artifactContract, error) {
	m, err := manifest.Load(path)
	if err != nil {
		return artifactContract{}, fmt.Errorf("load manifest: %w", err)
	}
	if errs := m.Validate(); errs != nil {
		return artifactContract{}, fmt.Errorf("manifest validation: %w", errs)
	}
	version := strings.TrimSpace(m.Release.FirecrackerVersion)
	digest, err := normalizeArtifactDigest(m.Release.KernelDigest)
	if err != nil {
		return artifactContract{}, fmt.Errorf("release.kernel_digest: %w", err)
	}
	if version == "" || strings.ContainsAny(version, "/\\\x00") {
		return artifactContract{}, fmt.Errorf("release.firecracker_version %q is not a valid storage-key component", version)
	}
	return artifactContract{key: "kernel/" + version, digest: digest}, nil
}

func normalizeArtifactDigest(raw string) (string, error) {
	digest := strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(digest), "sha256:") {
		digest = digest[len("sha256:"):]
	}
	if len(digest) != sha256.Size*2 {
		return "", fmt.Errorf("want 64 hexadecimal characters")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", fmt.Errorf("want 64 hexadecimal characters")
	}
	return strings.ToLower(digest), nil
}

func publishArtifact(ctx context.Context, be storage.StorageBackend, key, expectedDigest, path string) (artifactReport, error) {
	expectedDigest, err := normalizeArtifactDigest(expectedDigest)
	if err != nil {
		return artifactReport{}, err
	}
	fileDigest, size, err := hashArtifactFile(path)
	if err != nil {
		return artifactReport{}, fmt.Errorf("hash %s: %w", path, err)
	}
	if fileDigest != expectedDigest {
		return artifactReport{}, fmt.Errorf("release kernel digest mismatch: file=%s manifest=%s", fileDigest, expectedDigest)
	}

	if report, err := verifyArtifact(ctx, be, key, expectedDigest); err == nil {
		report.Operation = "publish"
		report.AlreadyPresent = true
		return report, nil
	} else if !storage.IsNotFound(err) {
		return artifactReport{}, fmt.Errorf("check existing %s: %w", key, err)
	}

	f, err := os.Open(path) //nolint:forbidigo // path is an explicit, operator-supplied release artifact.
	if err != nil {
		return artifactReport{}, fmt.Errorf("open %s: %w", path, err)
	}
	putErr := be.Put(ctx, key, f)
	closeErr := f.Close()
	if putErr != nil {
		return artifactReport{}, fmt.Errorf("put %s: %w", key, putErr)
	}
	if closeErr != nil {
		return artifactReport{}, fmt.Errorf("close %s: %w", path, closeErr)
	}

	report, err := verifyArtifact(ctx, be, key, expectedDigest)
	if err != nil {
		return artifactReport{}, fmt.Errorf("verify published %s: %w", key, err)
	}
	report.Operation = "publish"
	report.Bytes = size
	return report, nil
}

func verifyArtifact(ctx context.Context, be storage.StorageBackend, key, expectedDigest string) (artifactReport, error) {
	expectedDigest, err := normalizeArtifactDigest(expectedDigest)
	if err != nil {
		return artifactReport{}, err
	}
	r, err := be.Get(ctx, key)
	if err != nil {
		return artifactReport{}, err
	}
	got, size, readErr := hashArtifactReader(r)
	closeErr := r.Close()
	if readErr != nil {
		return artifactReport{}, fmt.Errorf("read %s: %w", key, readErr)
	}
	if closeErr != nil {
		return artifactReport{}, fmt.Errorf("close %s: %w", key, closeErr)
	}
	if got != expectedDigest {
		return artifactReport{}, fmt.Errorf("artifact digest mismatch: key=%s got=%s manifest=%s", key, got, expectedDigest)
	}
	return artifactReport{Operation: "verify", Key: key, Bytes: size, SHA256: got}, nil
}

func hashArtifactFile(path string) (string, int64, error) {
	f, err := os.Open(path) //nolint:forbidigo // path is an explicit, operator-supplied release artifact.
	if err != nil {
		return "", 0, err
	}
	digest, size, readErr := hashArtifactReader(f)
	closeErr := f.Close()
	if readErr != nil {
		return "", 0, readErr
	}
	if closeErr != nil {
		return "", 0, closeErr
	}
	return digest, size, nil
}

func hashArtifactReader(r io.Reader) (string, int64, error) {
	h := sha256.New()
	size, err := io.Copy(h, r)
	if err != nil {
		return "", size, err
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}

// validateArtifactStorageContract keeps this command fail-closed even when
// it is invoked outside node_join or cd-compute. Publishing a kernel to a
// runner-local filesystem would report success while leaving split compute
// nodes unable to wake from the shared-artifact path.
func validateArtifactStorageContract() error {
	if os.Getenv("FAAS_STORAGE_BACKEND") != "oci" {
		return fmt.Errorf("shared artifact operations require FAAS_STORAGE_BACKEND=oci")
	}
	shared := strings.TrimSpace(os.Getenv("FAAS_REQUIRE_SHARED_ARTIFACTS"))
	if !strings.EqualFold(shared, "1") && !strings.EqualFold(shared, "true") {
		return fmt.Errorf("shared artifact operations require FAAS_REQUIRE_SHARED_ARTIFACTS=1")
	}
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("FAAS_STORAGE_LOCAL_PREFIXES")), "none") {
		return fmt.Errorf("shared artifact operations require FAAS_STORAGE_LOCAL_PREFIXES=none")
	}
	return nil
}

// loadStorageEnv parses the small KEY=VALUE contract without invoking a
// shell. That keeps registry passwords opaque to shell expansion and avoids
// allowing a secret-bearing deployment file to execute code on the runner.
// The returned cleanup restores the process environment for in-process tests.
func loadStorageEnv(path string) (func(), error) {
	f, err := os.Open(path) //nolint:forbidigo // path is the explicit operator storage contract.
	if err != nil {
		return func() {}, fmt.Errorf("read storage env: %w", err)
	}
	defer func() { _ = f.Close() }()

	allowed := make(map[string]struct{}, len(storageEnvNames))
	for _, name := range storageEnvNames {
		allowed[name] = struct{}{}
	}
	previous := make(map[string]*string, len(storageEnvNames))
	for _, name := range storageEnvNames {
		if value, ok := os.LookupEnv(name); ok {
			valueCopy := value
			previous[name] = &valueCopy
		}
		if err := os.Unsetenv(name); err != nil {
			return func() {}, fmt.Errorf("clear %s: %w", name, err)
		}
	}
	restore := func() {
		for _, name := range storageEnvNames {
			if value, ok := previous[name]; ok {
				_ = os.Setenv(name, *value)
			} else {
				_ = os.Unsetenv(name)
			}
		}
	}

	scanner := bufio.NewScanner(f)
	seen := make(map[string]struct{}, len(storageEnvNames))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		name, value, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !ok || !storageEnvName.MatchString(name) {
			restore()
			return func() {}, fmt.Errorf("invalid storage env assignment %q", name)
		}
		if _, ok := allowed[name]; !ok {
			restore()
			return func() {}, fmt.Errorf("unsupported storage env assignment %q", name)
		}
		if _, ok := seen[name]; ok {
			restore()
			return func() {}, fmt.Errorf("duplicate storage env assignment %q", name)
		}
		seen[name] = struct{}{}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"')) {
			value = value[1 : len(value)-1]
		}
		if err := os.Setenv(name, value); err != nil {
			restore()
			return func() {}, fmt.Errorf("set %s: %w", name, err)
		}
	}
	if err := scanner.Err(); err != nil {
		restore()
		return func() {}, fmt.Errorf("scan storage env: %w", err)
	}
	return restore, nil
}
