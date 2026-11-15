package gateway

// adr: 20261110210000

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/internalsvc"
)

func TestInternalSvcTokenForOtherApp(t *testing.T) {
	priv, pub, err := internalsvc.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	kid := internalsvc.KidFromPub(pub)
	forA, err := internalsvc.Mint("schedd", time.Minute, map[string]any{"app_id": "app-a"}, priv, kid)
	if err != nil {
		t.Fatal(err)
	}
	noApp, err := internalsvc.Mint("schedd", time.Minute, nil, priv, kid)
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		token, app string
		want       bool
	}{
		"its own app":    {forA, "app-a", false},
		"another app":    {forA, "app-b", true},
		"no app claim":   {noApp, "app-b", false},
		"malformed jws":  {"not.a-token", "app-a", false},
		"bad base64 jws": {"a.!!!.c", "app-a", false},
	} {
		if got := internalSvcTokenForOtherApp(tc.token, tc.app); got != tc.want {
			t.Errorf("%s = %v, want %v", name, got, tc.want)
		}
	}
}
