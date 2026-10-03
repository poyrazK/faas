//go:build linux

// adr: 521 — binding readiness requires exact kernel mount identity and access flags.
package fcvm

import (
	"strings"
	"testing"
)

func TestNativeImageMountProofRejectsChangedAccessAndAmbiguousMounts(t *testing.T) {
	point := "/jail/image"
	good := "123 1 8:1 /source /jail/image ro,nosuid,nodev,noexec - ext4 /dev/sda1 rw\n"
	if err := checkNativeImageMountFlags([]byte(good), point, true); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{
		strings.Replace(good, "ro,nosuid", "rw,nosuid", 1),
		strings.Replace(good, ",noexec", "", 1),
		strings.Replace(good, ",nodev", "", 1),
		strings.Replace(good, ",nosuid", "", 1),
		strings.Replace(good, "ro,nosuid", "ro,rw,nosuid", 1),
		good + good,
		good + strings.Replace(good, point, point+"/nested", 1),
		"malformed\n",
	} {
		if err := checkNativeImageMountFlags([]byte(data), point, true); err == nil {
			t.Fatalf("changed kernel proof accepted: %s", data)
		}
	}
	if err := checkNativeImageMountFlags([]byte(good), point, false); err == nil {
		t.Fatal("read-only mount satisfied a writable binding")
	}
}
