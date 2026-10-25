package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func guardedHostAgePruneTo(dir string, overlap int, force, promote, dry bool, check func(string, bool) error) hostAgePruneReport {
	preview := hostAgePruneTo(dir, overlap, force, promote, true)
	preview.DryRun = dry
	if !preview.WouldApply || promote {
		return hostAgePruneTo(dir, overlap, force, promote, dry)
	}
	if err := check(dir, dry); err != nil {
		preview.Decision, preview.Error = "refused-secret-dependencies", err.Error()
		preview.Applied, preview.WouldApply = false, false
		return preview
	}
	if inspectHostAgeFile(hostAgePaths(dir).previous).SHA256 != preview.Previous.SHA256 {
		preview.Decision, preview.Error = "refused-key-changed", "previous identity changed during retirement checks"
		preview.Applied, preview.WouldApply = false, false
		return preview
	}
	return hostAgePruneTo(dir, overlap, force, promote, dry)
}

func checkHostAgeSecretRetirement(dir string, dry bool) error {
	previous, err := secretbox.LoadHostKey(hostAgePaths(dir).previous)
	if err != nil {
		return errors.New("cannot load the previous identity for encrypted release retirement checks")
	}
	identities, err := secretbox.LoadFleetAndHostKeys(filepath.Clean(dir))
	if errors.Is(err, secretbox.ErrHostKeyNotFound) {
		identities, err = secretbox.LoadHostKeys(dir)
	}
	if err != nil || len(identities) == 0 {
		return errors.New("cannot identify the current encryption key")
	}
	kid := previous.Recipient().String()
	if kid == identities[0].Recipient().String() {
		return errors.New("cannot retire the current encryption key")
	}
	pool, err := openFleetSealPool("", "")
	if err != nil {
		return errors.New("database access is required to verify encrypted release key dependencies")
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return retireHostAgeReleaseSecrets(ctx, state.NewPgStore(pool), kid, dry)
}

func retireHostAgeReleaseSecrets(ctx context.Context, store state.SecretRekeyStore, kid string, dry bool) error {
	usage, err := store.SecretEncryptionKeyUsage(ctx, kid)
	if err != nil {
		return errors.New("cannot verify encrypted release key dependencies")
	}
	if !usage.Empty() {
		return fmt.Errorf("encryption key remains required: %d live secrets, %d staged bundles, %d deployment snapshots, %d release captures", usage.LiveSecrets, usage.StagedBundles, usage.DeploymentSnapshots, usage.ReleaseCaptures)
	}
	if dry {
		return nil
	}
	if err := store.RetireSecretEncryptionKey(ctx, kid); err != nil {
		return errors.New("encrypted release key retirement was refused; retry after rekeying outstanding documents")
	}
	return nil
}
