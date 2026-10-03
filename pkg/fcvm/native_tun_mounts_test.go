// adr: 459 — TUN mount receipts require device access and exact target identity.
package fcvm

import (
	"strings"
	"testing"
)

func TestNativeTunMountProofRejectsChangedAccessAndAmbiguousMounts(t *testing.T) {
	point := "/jail/faas-host-tun"
	good := "125 1 0:7 /net/tun /jail/faas-host-tun rw,nosuid,noexec - devtmpfs devtmpfs rw\n"
	if err := checkNativeTunMountFlags([]byte(good), point); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{
		strings.Replace(good, "rw,nosuid", "ro,nosuid", 1),
		strings.Replace(good, ",noexec", "", 1),
		strings.Replace(good, ",nosuid", "", 1),
		strings.Replace(good, "rw,nosuid", "rw,nodev,nosuid", 1),
		strings.Replace(good, "rw,nosuid", "ro,rw,nosuid", 1),
		good + good,
		good + strings.Replace(good, point, point+"/nested", 1),
		strings.Replace(good, "faas-host-tun rw", "faas-host-tun\\040(deleted) rw", 1),
		strings.Replace(good, "125 1", "0 1", 1),
		"malformed\n",
	} {
		if err := checkNativeTunMountFlags([]byte(data), point); err == nil {
			t.Fatalf("changed kernel proof accepted: %s", data)
		}
	}
}
