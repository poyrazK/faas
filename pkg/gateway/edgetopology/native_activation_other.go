//go:build !linux

package edgetopology

import "context"

func newNativeActivationSession(context.Context, NativeActivationReview) (nativeActivationSession, error) {
	return nil, nativeReadError("Linux systemd activation audit")
}
