// commands_backup_unseal_archive.go — operator-side unseal leaf
// for the S3 archive credentials envelope (issue #562). Sibling
// of commands_backup_unseal_rclone.go (storage-box rclone.conf
// envelope, issue #250 / ADR-056).
//
// The namespace is `gregalectl backup` and the leaf today is:
//
//   - unseal-archive-creds  — decrypt a host.age-sealed
//     `archive-creds.json` envelope using the on-box age identity
//     and write the plaintext to
//     /etc/faas/secrets/storage-box/archive-creds.json (mode
//     0400 root:root so only the apid systemd unit can read it
//     via LoadCredential=).
//
// Mirrors the unseal-rclone leaf's posture exactly: refuses to
// overwrite an existing plaintext unless --force is passed
// (re-unsealing is a deliberate rotation step, not a
// bootstrap-time side effect). The age identity path defaults
// to /etc/faas/secrets/storage-box/box-age-key — the canonical
// install site written by the v1 bootstrap.sh step 11d
// (RETIRED 2026-08-15 by issue #911 / PR-1; v2 path is PR-X
// `gregalectl secrets init`).
//
// The wire shape is `logarchive.Credentials`, shared by apid and
// gatewayd-internal. Bucket may remain in FAAS_LOG_ARCHIVE_BUCKET for
// backwards compatibility; an empty bucket disables the shipper.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/logarchive"
)

// Default install paths for the S3 archive credentials envelope
// (issue #562). Matches the apid wire-up's
// /etc/faas/secrets/storage-box/archive-creds.json expectation
// and the v1 bootstrap.sh step 11d staging convention (RETIRED
// 2026-08-15 by issue #911 / PR-1; v2 path is PR-X `gregale
// secrets init`).
const (
	defaultArchiveCredsPath = defaultStorageBoxDir + "/archive-creds.json"
	defaultArchiveAgeIn     = "/root/archive-creds.json.age"
)

const subUnsealArchiveCreds = "unseal-archive-creds"

type unsealArchiveCredsFlags struct {
	ageIdentity string
	in          string
	out         string
	force       bool
}

func newUnsealArchiveCredsFlags(name string) (*flag.FlagSet, *unsealArchiveCredsFlags) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	f := &unsealArchiveCredsFlags{}
	fs.StringVar(&f.ageIdentity, "age-identity", defaultBoxAgeKey,
		"path to the box-local age identity used to decrypt the archive-creds.json envelope")
	fs.StringVar(&f.in, "in", defaultArchiveAgeIn,
		"path to the host.age-sealed archive-creds.json envelope (typically scp'd to /root/archive-creds.json.age)")
	fs.StringVar(&f.out, "out", defaultArchiveCredsPath,
		"path to write the decrypted archive-creds.json (mode 0400 root:root)")
	fs.BoolVar(&f.force, "force", false,
		"overwrite an existing plaintext archive-creds.json (rotation flow only)")
	return fs, f
}

// cmdBackupUnsealArchiveCreds decrypts a host.age-sealed
// archive-creds.json envelope using the box-local age identity
// and writes the plaintext to /etc/faas/secrets/storage-box/
// archive-creds.json (mode 0400 root:root). The v1 bootstrap.sh
// step 11d handshake (RETIRED 2026-08-15 by issue #911 / PR-1;
// v2 path is PR-X `gregalectl secrets init`): the operator scp's the
// .age envelope to /root/, this command unseals it, then
// shreds the envelope so a future host.age-key compromise
// can't replay it.
//
// Refuses to overwrite an existing plaintext unless --force
// is passed. The atomic-write dance (tmp + rename) avoids
// half-written plaintexts on the canonical install path.
// Mode 0400 root:root matches the apid systemd unit's
// LoadCredential= expectation — the file is read by apid at
// boot only, so the broad group-read the rclone.conf envelope
// needs (postgres user) is intentionally absent here.
func cmdBackupUnsealArchiveCreds(args []string) int {
	fs, f := newUnsealArchiveCredsFlags("backup unseal-archive-creds")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregalectl backup unseal-archive-creds [flags]", "backup")
		return 1
	}
	if err := unsealArchiveCreds(f); err != nil {
		return printErr("unseal failed", err)
	}
	PrintOK(osStdout, "Wrote %s (0400 root:root)\n  Next: systemctl daemon-reload && systemctl restart faas-apid",
		f.out)
	return 0
}

// unsealArchiveCreds mirrors unsealRclone's flow verbatim,
// swapping the destination mode (0400 root:root — apid reads
// it via LoadCredential=) and adding a JSON-shape sanity
// check that fails closed if the decrypted bytes don't parse
// as the expected logarchive.Credentials shape.
//
// The JSON-shape sanity check is new vs unseal-rclone: a
// half-decrypted or wrong-key archive-creds.json would
// silently ship garbage to S3, so we surface the failure at
// unseal time rather than at the first PUT. A bad rclone.conf
// is much more visible (the push unit hangs on the next
// schedule).
func unsealArchiveCreds(f *unsealArchiveCredsFlags) error {
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
	identity, err := parseBoxAgeIdentity(identityData)
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

	// Read the full plaintext so we can both JSON-validate and
	// atomic-write it. The envelope is small (≤2 KiB) so the
	// read-into-memory posture is safe.
	plaintext, err := io.ReadAll(plaintextR)
	if err != nil {
		return fmt.Errorf("read plaintext: %w", err)
	}

	// JSON-shape sanity check (see cmdBackupUnsealArchiveCreds
	// comment). A bug here would silently fail the apid boot,
	// so we surface the parse error at unseal time.
	var probe logarchive.Credentials
	if err := json.Unmarshal(plaintext, &probe); err != nil {
		return fmt.Errorf("plaintext is not a valid archive-creds.json: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(f.out), ".archive-creds.json.tmp.*")
	if err != nil {
		return fmt.Errorf("create tmp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		// Best-effort cleanup if rename didn't happen.
		_ = os.Remove(tmpName)
	}()

	if _, err := tmp.Write(plaintext); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write plaintext: %w", err)
	}
	if err := tmp.Chmod(0o400); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod 0400: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpName, f.out); err != nil {
		return fmt.Errorf("rename into place: %w", err)
	}
	return nil
}
