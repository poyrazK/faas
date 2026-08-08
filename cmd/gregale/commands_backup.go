// commands_backup.go — operator-side CLI for off-host pg backup
// wiring (issue #250 / ADR-056). Sibling surface to commands_sign_keys
// (cosign keypair) and commands_pki (mTLS trust root): every leaf is
// a local file-system operation against the canonical
// /etc/faas/secrets/storage-box/ paths (or caller-supplied
// --in / --out flags). No authedClient() call, no API hit.
//
// The namespace is `gregale backup` with one leaf today:
//
//   - unseal-rclone  — decrypt a host.age-sealed `rclone.conf` envelope
//     using the on-box age identity (box-age-key) and write the
//     plaintext to /etc/faas/secrets/storage-box/rclone.conf (mode
//     0440 root:postgres so the archive_command's rclone process can
//     read it). Refuses to overwrite an existing plaintext unless
//     --force is passed (re-unsealing is a deliberate rotation
//     step, not a bootstrap-time side effect).
//
// The age identity path defaults to
// /etc/faas/secrets/storage-box/box-age-key (the canonical install
// site written by bootstrap.sh step 11d). The input envelope path
// defaults to /root/rclone.conf.age — the staging location where
// bootstrap.sh expects the operator's scp to land. Output defaults
// to /etc/faas/secrets/storage-box/rclone.conf.
//
// The unseal deliberately uses the locally-stored age identity (NOT
// the on-host host.age key) so the on-disk `rclone.conf` can be
// re-sealed and rotated independently of the host.age key that
// protects per-account TOTP envelopes. Two secrets, two identities,
// two rotation cadences — same shape as the cosign sign-keypair
// (commands_sign_keys.go) being separate from the host.age.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"filippo.io/age"
)

// Canonical install paths for off-host pg backup secrets. These
// match the bootstrap.sh step 11d staging convention and the
// ansible role's stat-assert (postgres_backup/tasks/main.yml).
const (
	defaultStorageBoxDir = "/etc/faas/secrets/storage-box"
	defaultRcloneConf    = defaultStorageBoxDir + "/rclone.conf"
	defaultBoxAgeKey     = defaultStorageBoxDir + "/box-age-key"
	defaultRcloneAgeIn   = "/root/rclone.conf.age"
)

const dispatchBackup = "backup"

const subUnsealRclone = "unseal-rclone"

func cmdBackup(args []string) int {
	parent, _ := lookupCliCommand("backup")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale backup <subcommand> [flags]\n  known subcommands: unseal-rclone, unseal-archive-creds", "backup")
		return 1
	}
	switch args[0] {
	case subUnsealRclone:
		return cmdBackupUnsealRclone(args[1:])
	case subUnsealArchiveCreds:
		return cmdBackupUnsealArchiveCreds(args[1:])
	default:
		sug, _ := suggestSubcommand(args[0], parent)
		fmt.Fprintf(os.Stderr, "gregale backup: unknown subcommand %q (known: unseal-rclone, unseal-archive-creds)\n", args[0])
		maybeSuggestSub(sug)
		return 1
	}
}

type unsealRcloneFlags struct {
	ageIdentity string
	in          string
	out         string
	force       bool
}

func newUnsealRcloneFlags(name string) (*flag.FlagSet, *unsealRcloneFlags) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	f := &unsealRcloneFlags{}
	fs.StringVar(&f.ageIdentity, "age-identity", defaultBoxAgeKey,
		"path to the box-local age identity used to decrypt the rclone.conf envelope")
	fs.StringVar(&f.in, "in", defaultRcloneAgeIn,
		"path to the host.age-sealed rclone.conf envelope (typically scp'd to /root/rclone.conf.age)")
	fs.StringVar(&f.out, "out", defaultRcloneConf,
		"path to write the decrypted rclone.conf (mode 0440 root:postgres)")
	fs.BoolVar(&f.force, "force", false,
		"overwrite an existing plaintext rclone.conf (rotation flow only)")
	return fs, f
}

// cmdBackupUnsealRclone decrypts a host.age-sealed rclone.conf
// envelope using the box-local age identity and writes the
// plaintext to /etc/faas/secrets/storage-box/rclone.conf (mode
// 0400 root:root). This is the unseal side of the bootstrap.sh
// step 11d handshake: the operator scp's the .age envelope to
// /root/, bootstrap.sh calls gregale backup unseal-rclone, then
// shreds the envelope so a future host.age-key compromise can't
// replay it.
//
// Refuses to overwrite an existing plaintext unless --force is
// passed. Mirrors the cosign sign-keys init flow (refuse rotation
// by default — the operator uses the rotate subcommand for that),
// but rolled into a single subcommand here because the unseal
// step has no public-key side to mirror.
func cmdBackupUnsealRclone(args []string) int {
	fs, f := newUnsealRcloneFlags("backup unseal-rclone")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale backup unseal-rclone [flags]", "backup")
		return 1
	}
	if err := unsealRclone(f); err != nil {
		return printErr("unseal failed", err)
	}
	PrintOK(osStdout, "Wrote %s (0440 root:postgres)\n  Next: chmod 0440 %s && chown root:postgres %s\n  Next: systemctl daemon-reload && systemctl restart faas-pg-basebackup-push",
		f.out, f.out, f.out)
	return 0
}

// unsealRclone reads the .age envelope, decrypts it with the
// box-local age identity, and atomically writes the plaintext to
// the destination. The atomic-write dance (tmp + rename) avoids
// half-written plaintexts on the canonical install path — a
// truncated rclone.conf makes the push unit hang on rclone's
// "config not found" message indefinitely, which is harder to
// spot than a missing file. Mode 0440 root:postgres on the final
// file matches the ansible stat-assert at
// postgres_backup/tasks/main.yml:145-180 (review F2): the postgres
// user (User= on the systemd postgresql.service) must read this
// file via its `archive_command`, so the unseal stages it with
// group read for postgres.
func unsealRclone(f *unsealRcloneFlags) error {
	if !f.force {
		if _, err := os.Stat(f.out); err == nil {
			return fmt.Errorf("refusing to overwrite existing %s (use --force for rotation)", f.out)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat destination: %w", err)
		}
	}

	identityData, err := os.ReadFile(f.ageIdentity)
	if err != nil {
		return fmt.Errorf("read age identity %s: %w", f.ageIdentity, err)
	}
	identity, err := age.ParseX25519Identity(string(identityData))
	if err != nil {
		return fmt.Errorf("parse age identity: %w", err)
	}

	envelopeData, err := os.ReadFile(f.in)
	if err != nil {
		return fmt.Errorf("read envelope %s: %w", f.in, err)
	}
	envelopeR := bytes.NewReader(envelopeData)

	plaintextR, err := age.Decrypt(envelopeR, identity)
	if err != nil {
		return fmt.Errorf("decrypt envelope (wrong box-age-key?): %w", err)
	}

	// Atomic write: tmp in the destination directory, rename(2)
	// into place. The destination directory must exist (bootstrap
	// creates it with mode 0700 root:root in step 11d before calling
	// this command). We don't MkdirAll here — a missing dir means
	// the role hasn't run, and silently creating it would mask
	// that failure mode from the operator.
	tmp, err := os.CreateTemp(filepath.Dir(f.out), ".rclone.conf.tmp.*")
	if err != nil {
		return fmt.Errorf("create tmp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		// Best-effort cleanup if rename didn't happen.
		_ = os.Remove(tmpName)
	}()

	if _, err := io.Copy(tmp, plaintextR); err != nil {
		// Best-effort close: the tmp file is unlinked by the
		// deferred os.Remove above; a stuck close on the error path
		// would only delay that cleanup. The error we surface is
		// the io.Copy error, not a close error — pinning the close
		// would mask the real failure.
		_ = tmp.Close()
		return fmt.Errorf("write plaintext: %w", err)
	}
	if err := tmp.Chmod(0o440); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod 0440: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpName, f.out); err != nil {
		return fmt.Errorf("rename into place: %w", err)
	}
	return nil
}
