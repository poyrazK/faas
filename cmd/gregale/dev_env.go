package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
)

const devEnvFileMaxBytes = 1 << 20

// devSecretClient is the small part of the API client needed by the developer
// config sync. Keeping this narrow makes the key-only planning behavior easy
// to test without putting plaintext values in fixtures or logs.
type devSecretClient interface {
	ListSecretsWithScope(context.Context, string, string) (api.AppSecretListResponse, error)
	SetSecretWithScope(context.Context, string, string, string, string) error
}

type devEnvSyncReport struct {
	Changed      bool
	Keys         int
	Existing     int
	Added        int
	ExistingKeys []string
	AddedKeys    []string
}

func (r devEnvSyncReport) progressLine() string {
	added := strings.Join(r.AddedKeys, ", ")
	if added == "" {
		added = "none"
	}
	existing := strings.Join(r.ExistingKeys, ", ")
	if existing == "" {
		existing = "none"
	}
	return fmt.Sprintf("developer config: syncing keys (added: %s; existing: %s; values hidden)", added, existing)
}

type devEnvSyncState struct {
	mu          sync.Mutex
	path        string
	fingerprint [sha256.Size]byte
	synced      bool
}

func resolveDevEnvFilePath(cwd, raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	path := raw
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	path, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("could not resolve %q: %w", raw, err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("refusing to follow symlink at %q", raw)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("refusing non-regular env file %q", raw)
	}
	return path, nil
}

func readDevEnvBytes(path string) ([]byte, error) {
	f, err := openCustomerFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, devEnvFileMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > devEnvFileMaxBytes {
		return nil, fmt.Errorf("env file exceeds %d-byte limit", devEnvFileMaxBytes)
	}
	return data, nil
}

func devEnvFileFingerprint(path string) ([sha256.Size]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return [sha256.Size]byte{}, fmt.Errorf("env file is not a regular file")
	}
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%d\x00%d\x00%d\n", info.Size(), info.ModTime().UnixNano(), info.Mode())
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

func readDevEnvFile(path string) ([]secretsPair, [sha256.Size]byte, error) {
	data, err := readDevEnvBytes(path)
	if err != nil {
		return nil, [sha256.Size]byte{}, err
	}

	pairs := make([]secretsPair, 0)
	seen := make(map[string]struct{})
	for lineNumber, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pair, parseErr := parseSecretsPair(line)
		if parseErr != nil {
			// Do not return parseErr: it includes the complete input line,
			// which could expose a secret in a CLI error.
			return nil, [sha256.Size]byte{}, fmt.Errorf("env file line %d must look like KEY=VALUE", lineNumber+1)
		}
		if _, duplicate := seen[pair.Key]; duplicate {
			return nil, [sha256.Size]byte{}, fmt.Errorf("env file line %d repeats key %q", lineNumber+1, pair.Key)
		}
		seen[pair.Key] = struct{}{}
		pairs = append(pairs, pair)
	}
	if len(pairs) == 0 {
		return nil, [sha256.Size]byte{}, fmt.Errorf("env file contains no KEY=VALUE entries")
	}
	fingerprint, err := devEnvFileFingerprint(path)
	if err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	return pairs, fingerprint, nil
}

// sync uploads the explicit developer config to the default secret scope.
// It is additive/update-only: omitted keys are left untouched, so a typo or a
// partial local file cannot delete a working remote environment. The values
// are sent only in the API request body and never rendered.
func (s *devEnvSyncState) sync(ctx context.Context, client devSecretClient, app, path string) (devEnvSyncReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pairs, fingerprint, err := readDevEnvFile(path)
	if err != nil {
		return devEnvSyncReport{}, err
	}
	if s.synced && s.path == path && s.fingerprint == fingerprint {
		return devEnvSyncReport{}, nil
	}

	list, err := client.ListSecretsWithScope(ctx, app, "")
	if err != nil {
		return devEnvSyncReport{}, fmt.Errorf("list developer config keys: %w", err)
	}
	existing := make(map[string]struct{}, len(list.Secrets))
	for _, secret := range list.Secrets {
		existing[secret.Key] = struct{}{}
	}
	report := devEnvSyncReport{Changed: true, Keys: len(pairs)}
	for _, pair := range pairs {
		if _, ok := existing[pair.Key]; ok {
			report.Existing++
			report.ExistingKeys = append(report.ExistingKeys, pair.Key)
		} else {
			report.Added++
			report.AddedKeys = append(report.AddedKeys, pair.Key)
		}
	}
	for _, pair := range pairs {
		if err := client.SetSecretWithScope(ctx, app, pair.Key, pair.Value, ""); err != nil {
			return devEnvSyncReport{}, fmt.Errorf("set developer config key %q: %w", pair.Key, err)
		}
	}
	s.path = path
	s.fingerprint = fingerprint
	s.synced = true
	return report, nil
}
