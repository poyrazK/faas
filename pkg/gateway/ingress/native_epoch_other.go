//go:build !linux

package ingress

import "context"

func captureNativeProcessEpoch(context.Context) (NativeProcessEpoch, error) {
	return NativeProcessEpoch{}, ErrNativeStartupUnverified
}
