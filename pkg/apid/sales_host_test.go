package apid_test

import (
	"github.com/onebox-faas/faas/pkg/apid"
	"testing"
)

func TestSalesHostIsAnExactPlatformHost(t *testing.T) {
	for _, tc := range []struct {
		host string
		want bool
	}{{"sales.gregale.dev", true}, {"SALES.GREGALE.DEV:443", true}, {"sales.gregale.dev.evil.test", false}, {"customer.gregale.dev", false}} {
		if got := apid.IsPlatformHost(tc.host, "gregale.dev"); got != tc.want {
			t.Fatalf("%s: %v", tc.host, got)
		}
	}
	if !apid.IsApidPath("/r/code") || apid.IsApidPath("/reports") {
		t.Fatal("referral root is not anchored")
	}
}
