// commands_backup_cp_state.go — off-host backup of the control plane's
// host-local state (ADR-1344 §8): the CA key, the apid HMAC keys, sealed.env
// and the per-daemon secrets, which live neither in PostgreSQL nor in the
// release bundle.
//
//   - seal-cp-state     — run on the control plane by faas-cp-state-backup:
//     encrypt the files the spec selects to the operator's age recipients.
//     The host cannot read its own backup.
//   - restore-cp-state  — run on a replacement host with the operator's
//     identity: decrypt a bundle into the warm standby's staging layout, which
//     deploy/ansible/cp_state_restore.yml then installs before bootstrap.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/cpstate"
)

const (
	subSealCPState    = "seal-cp-state"
	subRestoreCPState = "restore-cp-state"

	defaultCPStateSpec       = "/etc/faas/cp-state-backup.paths"
	defaultCPStateRecipients = "/etc/faas/cp-state-backup.recipients"
	defaultCPStateStage      = "/var/lib/faas-standby"
)

func cmdBackupSealCPState(args []string) int {
	fs := flag.NewFlagSet("backup "+subSealCPState, flag.ContinueOnError)
	specPath := fs.String("paths", defaultCPStateSpec, "file listing the state paths, one per line; !path excludes")
	recipientsPath := fs.String("recipients", defaultCPStateRecipients, "file of operator age recipients (age1…), one per line")
	out := fs.String("out", "", "bundle to write (mode 0600); required")
	primary := fs.String("primary", "", "name recorded as the source host (default: hostname)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *out == "" {
		fmt.Fprintln(osStderr, "gregalectl backup seal-cp-state: --out is required")
		return 2
	}
	if err := sealCPState(*specPath, *recipientsPath, *out, *primary); err != nil {
		fmt.Fprintf(osStderr, "gregalectl backup seal-cp-state: %v\n", err)
		return 1
	}
	return 0
}

func sealCPState(specPath, recipientsPath, out, primary string) error {
	specFile, err := os.Open(specPath)
	if err != nil {
		return fmt.Errorf("open spec: %w", err)
	}
	defer specFile.Close()
	spec, err := cpstate.ParseSpec(specFile)
	if err != nil {
		return err
	}
	recipientsFile, err := os.Open(recipientsPath)
	if err != nil {
		return fmt.Errorf("open recipients: %w", err)
	}
	defer recipientsFile.Close()
	recipients, err := age.ParseRecipients(recipientsFile)
	if err != nil {
		return fmt.Errorf("parse recipients: %w", err)
	}
	if primary == "" {
		if primary, err = os.Hostname(); err != nil {
			return fmt.Errorf("hostname: %w", err)
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(out), ".cp-state-*.tmp")
	if err != nil {
		return fmt.Errorf("create bundle: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("protect bundle: %w", err)
	}
	index, err := cpstate.Seal(tmp, spec, recipients, primary, time.Now())
	if err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write bundle: %w", err)
	}
	if err := os.Rename(tmp.Name(), out); err != nil {
		return fmt.Errorf("install bundle: %w", err)
	}
	fmt.Fprintf(osStdout, "sealed %d files and %d directories from %s into %s\n", len(index.Files), len(index.Directories), primary, out)
	return nil
}

func cmdBackupRestoreCPState(args []string) int {
	fs := flag.NewFlagSet("backup "+subRestoreCPState, flag.ContinueOnError)
	identityPath := fs.String("identity", "", "operator age identity file; required")
	in := fs.String("in", "", "bundle to restore; required")
	stage := fs.String("stage", defaultCPStateStage, "staging directory the takeover installs from")
	force := fs.Bool("force", false, "replace an existing stage")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *identityPath == "" || *in == "" {
		fmt.Fprintln(osStderr, "gregalectl backup restore-cp-state: --identity and --in are required")
		return 2
	}
	if err := restoreCPState(*identityPath, *in, *stage, *force); err != nil {
		fmt.Fprintf(osStderr, "gregalectl backup restore-cp-state: %v\n", err)
		return 1
	}
	return 0
}

func restoreCPState(identityPath, in, stage string, force bool) error {
	identityFile, err := os.Open(identityPath)
	if err != nil {
		return fmt.Errorf("open identity: %w", err)
	}
	defer identityFile.Close()
	identities, err := age.ParseIdentities(identityFile)
	if err != nil {
		return fmt.Errorf("parse identity: %w", err)
	}
	if len(identities) == 0 {
		return errors.New("identity file holds no age identity")
	}
	bundle, err := os.Open(in)
	if err != nil {
		return fmt.Errorf("open bundle: %w", err)
	}
	defer bundle.Close()
	index, err := cpstate.Restore(bundle, identities, stage, force)
	if err != nil {
		return err
	}
	fmt.Fprintf(osStdout, "staged %d files from %s (sealed %s) in %s; install them with deploy/ansible/cp_state_restore.yml before bootstrap\n",
		len(index.Files), index.Primary, index.CreatedAt.Format(time.RFC3339), stage)
	return nil
}
