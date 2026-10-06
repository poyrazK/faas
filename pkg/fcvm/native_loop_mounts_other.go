//go:build !linux

package fcvm

func newNativeLoopMountBackend() nativeLoopMountBackend { return nil }

func nativeLoopDirectoryIdentity(path string) (nativeLoopIdentity, error) {
	return nativeLoopPortableDirectoryIdentity(path)
}
