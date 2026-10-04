//go:build !linux && metal

package leakcheck

func nativeImageMountLeaks() []error { return nil }
