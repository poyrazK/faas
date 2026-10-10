package fcvm

// WithNativeImageStagingRoot configures persistent ownership of temporary image
// names on an ext4/XFS/Btrfs disk. Configure before WithNativeProcessRecovery.
// This opt-in does not enable capture, restore or production qualification.
func (v *JailerVMM) WithNativeImageStagingRoot(directory string) *JailerVMM {
	v.nativeImageStagingRoot = directory
	return v
}
