package events

import "testing"

// The boot_failures alert metric leaves out mirror-mode boots by the
// payload's mirror key, which is written only for them.
func TestBootFailedPayloadMirror(t *testing.T) {
	if _, ok := (BootFailed{AppID: "a"}).Payload()["mirror"]; ok {
		t.Fatal("a live boot's payload carries mirror")
	}
	if got := (BootFailed{AppID: "a", Mirror: true}).Payload()["mirror"]; got != true {
		t.Fatalf("mirror boot payload mirror = %v, want true", got)
	}
}
