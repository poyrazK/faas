//go:build !linux

package overlaymetadata

func readNativeRootXattr(string, string, []byte) (int, error) {
	return 0, ErrUnsupported
}
