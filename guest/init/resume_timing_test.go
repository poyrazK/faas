// adr: 022
package main

import (
	"bytes"
	"testing"
	"time"
)

// The trailer layout is a wire contract with pkg/fcvm: marker, version, then
// four uint16 BE milliseconds, clamped rather than wrapped.
func TestResumeTimingTrailer(t *testing.T) {
	got := resumeTiming{
		read: 3 * time.Millisecond, kernel: 12 * time.Millisecond,
		reseed: 70 * time.Second, afterRestore: -time.Millisecond,
	}.appendTrailer([]byte{0, 1})
	want := []byte{0, 1, 'T', 1, 0, 3, 0, 12, 0xff, 0xff, 0, 0}
	if !bytes.Equal(got, want) {
		t.Fatalf("trailer = %v, want %v", got, want)
	}
}
