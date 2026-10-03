//go:build !linux && metal

package leakcheck

func nativeLoopLeaks() []error { return nil }
