// commands_secrets_init.go — operator-side CLI for the
// post-bootstrap secrets initialisation (issue #911 / ADR-110 PR-X).
//
// v1 (RETIRED 2026-08-15) wrote the cluster's four secrets from
// deploy/controlplane/bootstrap.sh step 11d. The script was
// deprecated when split-box deployment landed (a single
// bootstrap.sh step cannot run on a non-control-plane box and a
// per-box step cannot describe the role-aware envelope layout).
// v2 is `gregalectl secrets init` — a per-host operation that
// writes the four secrets to canonical paths and (optionally)
// persists compute_nodes.host_certificate + cert_fingerprint via
// the releaseinstall.Store.
//
// The namespace `gregalectl secrets` has four leaves today:
//
//   - init   — write host.age/box-age-key/session.key/rclone.conf/
//              archive-creds as a single batch. Refuses overwrite
//              unless --force is passed, or --preserve-existing is
//              used by an idempotent deployment retry. When a vmmd
//              TLS leaf and FAAS_PG_DSN are available, stamps
//              compute_nodes.{host_certificate, cert_fingerprint}
//              (soft warning on connectivity failure).
//   - rotate — host.age-only rotation (mirrors `host-age rotate`;
//              the other four secrets are not rotated through this
//              leaf — they have their own rotation runbooks).
//   - status — print mode/mtime/sha256 for each of the five files.
//              Missing files print an explicit "missing" line and
//              the leaf returns 0 (operator should see all paths).
//   - stamp — read the existing vmmd TLS leaf without changing it and persist
//             compute_nodes.host_certificate + cert_fingerprint. This is
//             the repair path for a node that was bootstrapped before the
//             database row existed; it never rotates or overwrites secrets.
//
// All five canonical paths are hard-coded — they match
// pkg/secretbox/hostkey.go, cmd/gregalectl/commands_backup.go, and
// cmd/gatewayd-internal/session_key.go. A reviewer changing one
// must update all five.

package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/releaseinstall"
	"github.com/onebox-faas/faas/pkg/secretbox"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Canonical installation paths for the four post-bootstrap secrets.
// Matches the v1 bootstrap.sh step 11d convention (RETIRED
// 2026-08-15 by issue #911 / PR-X; v2 path is this CLI).
//
// defaultBoxAgeKey + defaultRcloneConf are shared with
// commands_backup.go (the canonical backup unseal paths).
const (
	defaultSecretsDir = "/etc/faas/secrets"
	defaultStorageDir = defaultSecretsDir + "/storage-box"
	defaultHostAgeDir = defaultSecretsDir
)

const dispatchSecrets = "secrets"

// Sentinel errors. Wrapped by the helpers; surfaced by printErr.
var (
	// ErrSecretsInitRequiresRoot — secrets are 0400 root:root (spec
	// §11). A non-root caller is always a misconfiguration; the
	// CLI refuses rather than silently writing a wrong-mode file.
	ErrSecretsInitRequiresRoot = errors.New("secrets init: requires root (secrets are 0400 root:root per spec §11)")
	// ErrSecretsInitRefuseOverwrite — refuses to overwrite an
	// existing secret without --force. Operator re-running init
	// mid-deploy is almost certainly making a mistake (silently
	// overwriting host.age strands every SealedSecret ever
	// written under the old key).
	ErrSecretsInitRefuseOverwrite = errors.New("secrets init: refusing to overwrite existing file (use --force for emergency re-init)")
)

// cmdSecretsDispatch is the parent fan-out. Mirrors the
// cmdSignKeys / cmdHostAge shape: zero args prints usage;
// init/rotate/status each fan to a leaf.
func cmdSecretsDispatch(args []string) int {
	if len(args) > 0 && (args[0] == flagHelpLong || args[0] == flagHelpShort) {
		PrintUsage(os.Stderr, "usage: gregalectl secrets <init|rotate|status|stamp> [flags]", "secrets")
		return 0
	}
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregalectl secrets <init|rotate|status|stamp> [flags]", "secrets")
		return 1
	}
	switch args[0] {
	case subInit:
		return cmdSecretsInit(args[1:])
	case subRotate:
		return cmdSecretsRotate(args[1:])
	case subStatus:
		return cmdSecretsStatus(args[1:])
	case subSecretsStamp:
		return cmdSecretsStamp(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregalectl secrets: unknown subcommand %q (known: init, rotate, status, stamp)\n", args[0])
		return 1
	}
}

const subSecretsStamp = "stamp"

// secretsInitFlags is the shared flag struct for the init leaf.
// host is the PostgreSQL compute_nodes.name lookup key.
// The role column is intentionally NOT here — the renderer
// (Commit 2) writes compute_nodes.role, not secrets init.
// --no-db is useful for file-only/local bootstrap flows that intentionally
// defer the compute_nodes attestation write.
type secretsInitFlags struct {
	dir              string
	force            bool
	preserveExisting bool
	noDB             bool
	host             string
	dsn              string
}

func newSecretsInitFlags(name string, defaultForce bool) (*flag.FlagSet, *secretsInitFlags) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	f := &secretsInitFlags{}
	fs.StringVar(&f.dir, "dir", defaultSecretsDir, "root secrets directory (default /etc/faas/secrets)")
	fs.BoolVar(&f.force, "force", defaultForce, "overwrite existing secret files (default false)")
	fs.BoolVar(&f.preserveExisting, "preserve-existing", false, "preserve existing secret files and create only missing files (deployment retry)")
	fs.BoolVar(&f.noDB, "no-db", false, "skip the compute_nodes.cert_fingerprint write")
	fs.StringVar(&f.host, "host", "", "compute_nodes.name to stamp (default: hostname from os.Hostname)")
	fs.StringVar(&f.dsn, "pg-dsn", "", "PostgreSQL DSN (default: $FAAS_PG_DSN)")
	return fs, f
}

// cmdSecretsInit writes the five secrets as a single batch. host.age is
// written first because it is the load-bearing sealed-secret identity; the
// compute-node attestation is read from the independent vmmd TLS leaf below.
func cmdSecretsInit(args []string) int {
	fs, f := newSecretsInitFlags("secrets init", false)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregalectl secrets init [flags]", "secrets")
		return 1
	}
	if err := secretsInit(f, osStdout); err != nil {
		return printErr("init failed", err)
	}
	PrintOK(osStdout, "Wrote 5 secret files to %s\n  Next: systemctl restart faas-vmmd faas-apid faas-meterd faas-githubd faas-gatewayd-internal faas-gatewayd-public", f.dir)
	return 0
}

// secretsInit is the package-private worker. Splitting it from
// cmdSecretsInit lets tests exercise the file-write side without
// the flag-parse boilerplate.
func secretsInit(f *secretsInitFlags, stdout io.Writer) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("%w (run with sudo or as the root user)", ErrSecretsInitRequiresRoot)
	}
	if err := os.MkdirAll(f.dir, 0o700); err != nil {
		return fmt.Errorf("mkdir secrets dir %s: %w", f.dir, err)
	}
	storageDir := filepath.Join(f.dir, "storage-box")
	if err := os.MkdirAll(storageDir, 0o750); err != nil {
		return fmt.Errorf("mkdir storage dir %s: %w", storageDir, err)
	}

	// Step 1: host.age (0400 root:root). The identity is preserved
	// across deployment retries, but it is not the compute-node TLS
	// certificate and must not be used for cert_fingerprint.
	hostAgePath := filepath.Join(f.dir, "host.age")
	_, err := writeOrLoadHostAge(hostAgePath, f.force, f.preserveExisting)
	if err != nil {
		return err
	}

	// Step 2: box-age-key (0400 root:root). The on-disk
	// content is the X25519 identity (mirrors what cmdBackup
	// reads at unseal time). It is consumed only by the root-run
	// unseal CLI, never by a daemon.
	boxAgePath := filepath.Join(storageDir, "box-age-key")
	if err := writeBoxAgeKey(boxAgePath, f.force, f.preserveExisting); err != nil {
		return err
	}

	// Step 3: session.key (0400 root:root). 32 random bytes,
	// hex-encoded (the gatewayd loader expects hex per
	// cmd/gatewayd-internal/session_key.go:32).
	sessionKeyPath := filepath.Join(f.dir, "session.key")
	if err := writeSessionKey(sessionKeyPath, f.force, f.preserveExisting); err != nil {
		return err
	}

	// Step 4: rclone.conf (0400 root:root). The init leaf
	// writes a stub envelope; the operator uses `backup
	// unseal-rclone` to overwrite with the real plaintext after
	// scp'ing the .age envelope. The stub is a single-line JSON
	// marker so the ansible stat-assert
	// (postgres_backup/tasks/main.yml) passes before the unseal
	// step.
	rclonePath := filepath.Join(storageDir, "rclone.conf")
	if err := writeRcloneStub(rclonePath, f.force, f.preserveExisting); err != nil {
		return err
	}

	// Step 5: archive-creds.json (0400 root:root). Empty envelope
	// — the operator uses `backup unseal-archive-creds` to fill
	// it. The mandatory-on-disk file is the existence + 0400 mode
	// (deploy/ansible/roles/log_archive/tasks/main.yml:39).
	archivePath := filepath.Join(storageDir, "archive-creds.json")
	if err := writeArchiveStub(archivePath, f.force, f.preserveExisting); err != nil {
		return err
	}

	// Step 6: stamp compute_nodes.{host_certificate,
	// cert_fingerprint} when FAAS_PG_DSN is set. The database
	// attestation is the vmmd mTLS leaf, matching vmmd's startup
	// guard and the doctor's drift check. host.age is a separate
	// customer-secret encryption identity.
	host := f.host
	if host == "" {
		h, herr := os.Hostname()
		if herr != nil {
			return fmt.Errorf("read hostname: %w", herr)
		}
		host = h
	}
	dsn := f.dsn
	if dsn == "" {
		dsn = resolveSecretsDSN()
	}
	if dsn != "" && !f.noDB {
		hostCertPEM, hostCertFP, certErr := loadComputeNodeCertificateAttestation()
		if certErr != nil {
			_, _ = fmt.Fprintf(stdout, "warning: compute node certificate attestation unavailable: %v\n", certErr)
		} else if err := writeComputeNodeCert(dsn, host, hostCertPEM, []byte(hostCertFP), stdout); err != nil {
			// Soft warning — the file writes succeeded; the DB
			// write is a downstream signal the doctor will
			// detect on the next run.
			_, _ = fmt.Fprintf(stdout, "warning: compute_nodes.cert_fingerprint write failed: %v\n", err)
		}
	}
	return nil
}

type secretsStampFlags struct {
	dir                 string
	host                string
	dsn                 string
	expectedFingerprint string
}

// cmdSecretsStamp is the non-destructive repair path for an already
// provisioned host. It deliberately does not call secretsInit: that leaf
// owns key generation and correctly refuses to overwrite existing files.
func cmdSecretsStamp(args []string) int {
	fs := flag.NewFlagSet("secrets stamp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("dir", defaultSecretsDir, "root secrets directory (default /etc/faas/secrets)")
	host := fs.String("host", "", "compute_nodes.name to stamp (default: hostname)")
	dsn := fs.String("pg-dsn", "", "PostgreSQL DSN (default: $FAAS_PG_DSN, $DATABASE_URL, or deploy env file)")
	expectedFingerprint := fs.String("expected-fingerprint", "", "CAS guard for an internal mTLS attestation rotation")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregalectl secrets stamp [flags]", "secrets")
		return 1
	}
	if err := stampExistingHostCertificate(&secretsStampFlags{dir: *dir, host: *host, dsn: *dsn, expectedFingerprint: *expectedFingerprint}); err != nil {
		return printErr("stamp failed", err)
	}
	hostName := *host
	if hostName == "" {
		hostName, _ = os.Hostname()
	}
	PrintOK(osStdout, "Stamped existing vmmd TLS certificate for compute node %s (no secret files changed)\n", hostName)
	return 0
}

// stampExistingHostCertificate verifies that host.age still exists, then
// loads the existing public vmmd TLS leaf and writes only the two database
// audit columns. Neither secret files nor certificates are rewritten.
func stampExistingHostCertificate(f *secretsStampFlags) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("secrets stamp: requires root to read host.age (run with sudo or as the root user)")
	}
	hostAgePath := filepath.Join(f.dir, "host.age")
	_, err := secretbox.LoadHostKey(hostAgePath)
	if err != nil {
		return fmt.Errorf("load existing host.age: %w", err)
	}
	// Keep the host.age existence check above: a node without its sealed-secret
	// identity is not a valid deployment target. The database certificate
	// attestation itself, however, must be the mTLS leaf that vmmd presents.
	hostCert, fingerprint, err := loadComputeNodeCertificateAttestation()
	if err != nil {
		return fmt.Errorf("load vmmd server certificate: %w", err)
	}
	host := f.host
	if host == "" {
		host, err = os.Hostname()
		if err != nil {
			return fmt.Errorf("read hostname: %w", err)
		}
	}
	dsn := f.dsn
	if dsn == "" {
		dsn = resolveSecretsDSN()
	}
	if dsn == "" {
		return fmt.Errorf("database DSN not set (use --pg-dsn, FAAS_PG_DSN, DATABASE_URL, or a deploy env file)")
	}
	if f.expectedFingerprint != "" {
		if err := writeComputeNodeCertCAS(dsn, host, f.expectedFingerprint, hostCert, fingerprint); err != nil {
			return err
		}
	} else if err := writeComputeNodeCert(dsn, host, hostCert, []byte(fingerprint), osStdout); err != nil {
		return err
	}
	return nil
}

// resolveSecretsDSN follows the same precedence as release install. The
// explicit flag is handled by each caller first; this helper only resolves
// deployment-provided environment sources.
func resolveSecretsDSN() string {
	for _, key := range []string{"FAAS_PG_DSN", "DATABASE_URL"} {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	for _, path := range []string{"/etc/faas/compute-db.env", "/etc/faas/sealed.env"} {
		if value, ok := readDatabaseEnvFile(path); ok {
			return value
		}
	}
	return ""
}

// enforceFileMode re-applies the requested mode after WriteFile.
// The umask may loosen the mode WriteFile requests, so a post-write
// Chmod is the only way to guarantee the contract on disk.
func enforceFileMode(path string, mode os.FileMode) error {
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("chmod %04o %s: %w", mode, path, err)
	}
	return nil
}

// writeHostAge calls secretbox.GenerateAndSaveHostKey to write a
// fresh X25519 identity with mode 0400.
func writeHostAge(path string, force bool) (*age.X25519Identity, error) {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return nil, fmt.Errorf("%w: %s", ErrSecretsInitRefuseOverwrite, path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("stat %s: %w", path, err)
		}
	}
	id, err := secretbox.GenerateAndSaveHostKey(path)
	if err != nil {
		return nil, err
	}
	if err := enforceFileMode(path, 0o400); err != nil {
		return nil, err
	}
	return id, nil
}

// writeOrLoadHostAge is the deployment-retry variant of writeHostAge. An
// existing identity is load-bearing state: preserve it and derive the
// fingerprint from the same bytes instead of rotating it during a retry.
func writeOrLoadHostAge(path string, force, preserveExisting bool) (*age.X25519Identity, error) {
	if preserveExisting {
		if _, err := os.Stat(path); err == nil {
			id, loadErr := secretbox.LoadHostKey(path)
			if loadErr != nil {
				return nil, fmt.Errorf("load existing host.age %s: %w", path, loadErr)
			}
			if modeErr := enforceFileMode(path, 0o400); modeErr != nil {
				return nil, modeErr
			}
			return id, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("stat %s: %w", path, err)
		}
	}
	return writeHostAge(path, force)
}

// writeBoxAgeKey writes a fresh X25519 identity with mode 0400
// root:root. Uses secretbox.GenerateAndSaveHostKey for the
// keygen (same shape as host.age); the file IS the right half
// (the file content is the identity string per filippo.io/age
// convention). The key is consumed only by the root-run unseal CLI.
func writeBoxAgeKey(path string, force, preserveExisting bool) error {
	if preserveExisting {
		if _, err := os.Stat(path); err == nil {
			return enforceFileMode(path, 0o400)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
	}
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%w: %s", ErrSecretsInitRefuseOverwrite, path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
	}
	if _, err := secretbox.GenerateAndSaveHostKey(path); err != nil {
		return err
	}
	if err := enforceFileMode(path, 0o400); err != nil {
		return err
	}
	return nil
}

// writeSessionKey writes a 32-byte random key, hex-encoded.
// Mode 0400 root:root; the gatewayd loader reads FAAS_SESSION_KEY
// and expects hex (cmd/gatewayd-internal/session_key.go:43).
func writeSessionKey(path string, force, preserveExisting bool) error {
	if preserveExisting {
		if _, err := os.Stat(path); err == nil {
			return enforceFileMode(path, 0o400)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
	}
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%w: %s", ErrSecretsInitRefuseOverwrite, path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
	}
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return fmt.Errorf("read random bytes: %w", err)
	}
	hexBytes := []byte(hex.EncodeToString(keyBytes))
	if err := os.WriteFile(path, hexBytes, 0o400); err != nil {
		return fmt.Errorf("write session.key %s: %w", path, err)
	}
	if err := enforceFileMode(path, 0o400); err != nil {
		return err
	}
	return nil
}

// writeRcloneStub writes a placeholder envelope. The operator
// replaces it with the real plaintext via `backup unseal-rclone`.
// The stub is a single line of JSON so the ansible stat-assert
// (postgres_backup/tasks/main.yml) passes. systemd reads the source as root
// and stages it for the PostgreSQL service, so the source stays root-only.
func writeRcloneStub(path string, force, preserveExisting bool) error {
	if preserveExisting {
		if _, err := os.Stat(path); err == nil {
			return enforceFileMode(path, 0o400)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
	}
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%w: %s", ErrSecretsInitRefuseOverwrite, path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
	}
	stub := []byte(`{"_":"secrets init stub — replace via 'gregalectl backup unseal-rclone'"}`)
	if err := os.WriteFile(path, stub, 0o400); err != nil {
		return fmt.Errorf("write rclone.conf stub %s: %w", path, err)
	}
	if err := enforceFileMode(path, 0o400); err != nil {
		return err
	}
	return nil
}

// writeArchiveStub writes an empty JSON envelope. The operator
// replaces it via `backup unseal-archive-creds`. The mandatory
// shape is empty object {} — the log-archive's LoadCredential
// (cmd/faas-apid/main.go / systemd unit) parses the file as JSON
// and an empty object is the canonical "no creds yet" state.
func writeArchiveStub(path string, force, preserveExisting bool) error {
	if preserveExisting {
		if _, err := os.Stat(path); err == nil {
			return enforceFileMode(path, 0o400)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
	}
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%w: %s", ErrSecretsInitRefuseOverwrite, path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
	}
	stub := []byte(`{}`)
	if err := os.WriteFile(path, stub, 0o400); err != nil {
		return fmt.Errorf("write archive-creds.json stub %s: %w", path, err)
	}
	if err := enforceFileMode(path, 0o400); err != nil {
		return err
	}
	return nil
}

// writeComputeNodeCert writes the host_certificate +
// cert_fingerprint columns. Mirrors the Write path in
// pkg/releaseinstall/store.go (PR-X / Commit 3).
func writeComputeNodeCert(dsn, host string, pem, fingerprint []byte, stdout io.Writer) error {
	pool, err := openPgPoolFromDSN(dsn)
	if err != nil {
		return fmt.Errorf("open pg pool: %w", err)
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store := releaseinstall.NewStore(pool)
	if err := store.StampHostCertificate(ctx, host, string(pem), string(fingerprint)); err != nil {
		return fmt.Errorf("stamp host cert: %w", err)
	}
	return nil
}

func writeComputeNodeCertCAS(dsn, host, expectedFingerprint string, pem []byte, fingerprint string) error {
	pool, err := openPgPoolFromDSN(dsn)
	if err != nil {
		return fmt.Errorf("open pg pool: %w", err)
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store := releaseinstall.NewStore(pool)
	if err := store.CompareAndSwapHostCertificate(ctx, host, expectedFingerprint, string(pem), fingerprint); err != nil {
		return fmt.Errorf("rotate host cert attestation: %w", err)
	}
	return nil
}

// cmdSecretsRotate is the host.age-only rotation. The other four
// secrets each have their own runbook (session.key is rotated by
// recycling the gatewayd pods; rclone.conf is rotated by
// re-unsealing; archive-creds by re-unsealing). This leaf is a
// thin wrapper around cmdHostAge's rotate path so the operator
// only needs to learn one namespace.
func cmdSecretsRotate(args []string) int {
	fs := flag.NewFlagSet("secrets rotate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("dir", defaultHostAgeDir, "host.age directory (default /etc/faas/secrets)")
	force := fs.Bool("force", true, "overwrite existing host.age (default true for rotate)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregalectl secrets rotate [flags]", "secrets")
		return 1
	}
	// Delegate to hostAgeRotate. The receipt is the same as
	// `gregalectl host-age rotate` — we want operators to learn
	// one rotate flow.
	_, _, err := hostAgeRotate(*dir, *force)
	if err != nil {
		return printErr("rotate failed", err)
	}
	return 0
}

// cmdSecretsStatus prints mode/mtime/sha256 for each of the
// five secret files. Mirrors reportHostAgeStatus / reportSignKeyStatus.
func cmdSecretsStatus(args []string) int {
	fs := flag.NewFlagSet("secrets status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("dir", defaultSecretsDir, "root secrets directory (default /etc/faas/secrets)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregalectl secrets status [flags]", "secrets")
		return 1
	}
	storageDir := filepath.Join(*dir, "storage-box")
	paths := []struct {
		label string
		path  string
	}{
		{"host.age       ", filepath.Join(*dir, "host.age")},
		{"session.key    ", filepath.Join(*dir, "session.key")},
		{"box-age-key    ", filepath.Join(storageDir, "box-age-key")},
		{"rclone.conf    ", filepath.Join(storageDir, "rclone.conf")},
		{"archive-creds  ", filepath.Join(storageDir, "archive-creds.json")},
	}
	for _, p := range paths {
		reportSecretStatus(osStdout, p.label, p.path)
	}
	return 0
}

// reportSecretStatus prints one line per file: <label>  <mode>
// <sha256[:12] of bytes>  <path>. Missing files print
// "<label>  missing: <path>" so the operator sees all paths even
// when one is uninitialised.
func reportSecretStatus(w io.Writer, label, path string) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintf(w, "%s  missing  %s\n", label, path)
			return
		}
		_, _ = fmt.Fprintf(w, "%s  stat error: %v  %s\n", label, err, path)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		_, _ = fmt.Fprintf(w, "%s  mode %#o  read error: %v  %s\n", label, info.Mode().Perm(), err, path)
		return
	}
	sum := sha256.Sum256(data)
	_, _ = fmt.Fprintf(w, "%s  %#o  sha256:%s  %s\n", label, info.Mode().Perm(), hex.EncodeToString(sum[:6]), path)
}

// sha256Hex is a small helper that returns sha256(<data>) as hex.
func sha256Hex(data []byte) []byte {
	sum := sha256.Sum256(data)
	out := make([]byte, hex.EncodedLen(len(sum)))
	hex.Encode(out, sum[:])
	return out
}

// openPgPoolFromDSN returns a pgxpool.Pool from an explicit DSN
// (does NOT consult FAAS_PG_DSN — callers that want env-fallback
// must resolve the DSN first; secrets init flags do exactly this).
// Used by writeComputeNodeCert; mirrors openPgPoolFromEnv in
// commands_release.go.
func openPgPoolFromDSN(dsn string) (*pgxpool.Pool, error) {
	if dsn == "" {
		return nil, fmt.Errorf("dsn is empty")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("open pgxpool: %w", err)
	}
	return pool, nil
}
