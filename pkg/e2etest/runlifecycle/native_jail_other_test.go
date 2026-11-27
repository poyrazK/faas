//go:build metal && !linux

// adr: 20261114010000 — native acceptance cannot be replaced by a non-Linux fixture.
package runlifecycle_test

import "testing"

func prepareNativeRunJail(t *testing.T, _ string) {
	t.Helper()
	t.Fatal("native recovery acceptance requires Linux")
}
