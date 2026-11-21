package executor

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
)

func stageStoredInputs(ctx context.Context, req executionproto.Request, dir string) error {
	for index, f := range req.InputFiles {
		if req.GuestFiles == nil || req.GuestFiles.Receive == nil {
			return errors.New("file receiver unavailable")
		}
		target := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		err = req.GuestFiles.Receive(ctx, index, file)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if err = os.Chown(target, guestUID, guestGID); err != nil {
			if err = os.Chmod(target, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}
func collectStoredArtifacts(ctx context.Context, dir string, req executionproto.Request) ([]api.ExecutionArtifact, error) {
	if req.GuestFiles == nil || req.GuestFiles.Send == nil {
		return nil, errors.New("file sender unavailable")
	}
	out := make([]api.ExecutionArtifact, 0, len(req.StoredOutputs))
	var total int64
	for index, selected := range req.StoredOutputs {
		f, err := openArtifact(dir, selected.Path)
		if err != nil {
			return nil, err
		}
		info, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		total += info.Size()
		if total > req.MaxArtifactBytes {
			_ = f.Close()
			return nil, executionproto.ErrOutputLimitExceeded
		}
		descriptor, err := req.GuestFiles.Send(ctx, index, f, info.Size())
		closeErr := f.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		out = append(out, api.ExecutionArtifact{Name: selected.Path, FileID: descriptor.ID, SizeBytes: int(descriptor.SizeBytes), SHA256: descriptor.SHA256})
	}
	return out, nil
}
