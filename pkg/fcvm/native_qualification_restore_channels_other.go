//go:build !linux

package fcvm

import (
	"context"
	"errors"
)

type unsupportedNativeQualificationRestoreChannels struct{}

func newNativeQualificationRestoreChannelBackend() nativeQualificationRestoreChannelBackend {
	return unsupportedNativeQualificationRestoreChannels{}
}
func (unsupportedNativeQualificationRestoreChannels) Pin(context.Context, nativeLaunchRecord) (nativeQualificationRestoreChannelPeer, error) {
	return nil, errors.New("native restore channels: original Linux process custody is required")
}
func (c *nativeQualificationRestoreChannels) require(context.Context, uint32) error {
	return errors.New("native restore channels: Linux target authority is required")
}
