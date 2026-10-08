//go:build !linux && !darwin

package globalingress

func CheckPublishDirectory(string) error { return ErrUnavailable }
func WriteManifest(string, []byte) error { return ErrUnavailable }
