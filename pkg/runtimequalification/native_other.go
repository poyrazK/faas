//go:build !linux

package runtimequalification

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"os/exec"
)

func CheckNativeCollectorHost(context.Context) error {
	return fmt.Errorf("collector requires designated native Linux amd64 KVM as root: %w", ErrEvidence)
}
func ReadNativeSigningSeed(string, ed25519.PublicKey) (ed25519.PrivateKey, error) {
	return nil, fmt.Errorf("protected signing key can only be loaded by native Linux owner: %w", ErrEvidence)
}
func OpenNativeSession(ctx context.Context, _ NativeConfig, _ Fixture) (NativeSession, error) {
	return nil, CheckNativeCollectorHost(ctx)
}
func configureNativeCommand(*exec.Cmd) {}
