//go:build !linux

package edgetopology

import "context"

func newNativeScopeSession(context.Context, NativeScopeReview) (nativeScopeSession, error) {
	return nil, nativeReadError("Linux procfs, pidfds and systemd")
}
