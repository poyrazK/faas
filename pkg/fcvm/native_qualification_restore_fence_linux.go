//go:build linux

package fcvm

import (
	"context"
	"errors"

	"golang.org/x/sys/unix"
)

type linuxNativeQualificationRestoreFenceBackend struct{}
type linuxNativeQualificationRestoreFence struct {
	input  *linuxNativeSnapshotMemoryIO
	normal uint64
}

func newNativeQualificationRestoreFenceBackend() nativeQualificationRestoreFenceBackend {
	return linuxNativeQualificationRestoreFenceBackend{}
}
func (linuxNativeQualificationRestoreFenceBackend) Pin(ctx context.Context, owner nativeLaunchRecord) (nativeQualificationRestoreFence, error) {
	input, err := openNativeSnapshotMemoryIOWithMode(ctx, owner, unix.O_RDONLY)
	if err != nil {
		return nil, err
	}
	normal, _, err := nativeSnapshotMemoryLimits(owner)
	if err != nil {
		return nil, errors.Join(err, input.Close())
	}
	return &linuxNativeQualificationRestoreFence{input: input, normal: normal}, nil
}
func (f *linuxNativeQualificationRestoreFence) Group() nativeHostHelperGroup { return f.input.group }
func (f *linuxNativeQualificationRestoreFence) Require(ctx context.Context) error {
	if err := f.input.Check(ctx); err != nil {
		return err
	}
	value, err := f.input.Read()
	if err != nil || value != f.normal {
		return errors.Join(err, errors.New("native restore load: original billable RAM fence changed"))
	}
	return errors.Join(f.input.Check(ctx), ctx.Err())
}
func (f *linuxNativeQualificationRestoreFence) Close() error { return f.input.Close() }
