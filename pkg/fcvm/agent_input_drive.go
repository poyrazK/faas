package fcvm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/agentrunproto"
)

// agentInputImageName is an agent run's input drive inside its chroot.
const agentInputImageName = "agent-input.img"

// stageAgentInputForOwner gives an agent run VM its input drive (ADR-20261027090010
// §3): a per-run file of exactly agentrunproto.InputDriveBytes holding the
// run's image, or zeros, then sparse zeros. The fixed capacity is what lets a
// VM restored from a preset snapshot use the drive the snapshot was captured
// with. The file is created on disk in dir, never in the tmpfs chroot,
// tracked for teardown, and bound into the jail read-only.
func (v *JailerVMM) stageAgentInputForOwner(ctx context.Context, owner nativeLaunchRecord, root, src, dir, instance string) (string, error) {
	f, err := v.newMaterialisedFile(instance, dir, layerCloneTempPattern(instance), "materialised")
	if err != nil {
		return "", fmt.Errorf("stage agent input: create: %w", err)
	}
	path := f.Name()
	if err := errors.Join(fillAgentInput(f, src), f.Close()); err != nil {
		return "", errors.Join(fmt.Errorf("stage agent input: %w", err), v.removeMaterialisedFile(path))
	}
	return v.bindImageForOwner(ctx, owner, root, path, agentInputImageName, instance, 0o044, true)
}

// fillAgentInput writes src's image, if any, then pads f with sparse zeros
// to the drive's fixed capacity.
func fillAgentInput(f *os.File, src string) error {
	if src != "" {
		in, err := os.Open(src) //nolint:forbidigo // src is the vmmd-resolved local copy of an input image key.
		if err != nil {
			return err
		}
		info, err := in.Stat()
		if err == nil && info.Size() > agentrunproto.InputDriveBytes {
			err = fmt.Errorf("image is %d bytes; the input drive holds %d", info.Size(), agentrunproto.InputDriveBytes)
		}
		if err == nil {
			_, err = io.Copy(f, in)
		}
		if err := errors.Join(err, in.Close()); err != nil {
			return err
		}
	}
	if err := f.Truncate(agentrunproto.InputDriveBytes); err != nil {
		return err
	}
	return f.Chmod(0o644)
}

// agentInputStagingDir is the directory of the VM's writable layer source:
// on disk, beside the files vmmd already materialises for the VM.
func agentInputStagingDir(drives []Drive) string {
	for _, d := range drives {
		if !d.IsReadOnly && d.PathOnHost != "" {
			return filepath.Dir(d.PathOnHost)
		}
	}
	return os.TempDir()
}
