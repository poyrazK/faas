package state

// adr: 435. Full-rootfs images still boot a distinct shared drive0.

import "github.com/onebox-faas/faas/pkg/imagechain"

// RuntimeBaseKeyForArch is shared by producer validation and scheduler wiring.
func RuntimeBaseKeyForArch(runtime, arch string) string {
	if runtime == "" {
		return "base/base-" + arch + ".ext4"
	}
	return "base/runner-" + runtime + "-" + arch + ".ext4"
}

func checkRegistryRuntimeDefaultBase(in DeploymentRegistryRootfsInput, base BaseImageProducer, runtime string) error {
	if in.Kind != "full-rootfs" {
		return nil
	}
	if in.BaseProducerID != base.ID || in.BaseInputHash != base.InputHash || in.LayerStart != 0 ||
		base.Input.Artifact.StorageKey != RuntimeBaseKeyForArch(runtime, imagechain.ImageArchitecture) || base.Input.GuestInitDigest == "" {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}
