// adr: 20261027090013 §3 — a sandbox token grants calls on one session of one
// account and tenant until it expires; a forged, altered or expired token
// grants nothing.
package sandboxtoken_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sandboxtoken"
)

func TestTokenGrantsItsSessionUntilExpiry(t *testing.T) {
	s, err := sandboxtoken.NewSigner(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0).UTC()
	claims := sandboxtoken.Claims{SandboxID: "sb-1", AccountID: "acct-1", TenantID: "tenant-1", IssuedAt: now, ExpiresAt: now.Add(time.Minute),
		NodeID: "node-a", InstanceID: "vm-1", ExecTimeoutMaxMillis: 300_000, ExecOutputMaxBytes: 1 << 20}
	token, err := s.Sign(claims)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Verify(token, now.Add(30*time.Second))
	if err != nil || got != claims || !got.Placed() {
		t.Fatalf("verify = %+v, %v; want %+v", got, err, claims)
	}
	if (sandboxtoken.Claims{SandboxID: "sb-1", NodeID: "node-a"}).Placed() {
		t.Fatal("a token without an instance routes to a VM")
	}
	other, _ := sandboxtoken.NewSigner(bytes.Repeat([]byte{4}, 32))
	head, rest, _ := strings.Cut(token, ".")
	payload, sig, _ := strings.Cut(rest, ".")
	flipped := "A"
	if strings.HasSuffix(sig, "A") {
		flipped = "B"
	}
	for name, tc := range map[string]struct {
		signer *sandboxtoken.Signer
		token  string
		at     time.Time
	}{
		"expired":           {s, token, now.Add(time.Minute)},
		"another key":       {other, token, now},
		"altered payload":   {s, head + "." + payload + "A." + sig, now},
		"another format":    {s, "sbx2." + payload + "." + sig, now},
		"no signature":      {s, head + "." + payload, now},
		"oversized":         {s, token + strings.Repeat("A", 1024), now},
		"empty":             {s, "", now},
		"another signature": {s, head + "." + payload + "." + sig[:len(sig)-1] + flipped, now},
	} {
		if _, err := tc.signer.Verify(tc.token, tc.at); !errors.Is(err, sandboxtoken.ErrToken) {
			t.Errorf("%s: verify = %v, want ErrToken", name, err)
		}
	}
	if _, err := sandboxtoken.NewSigner([]byte("short")); err == nil {
		t.Fatal("a short secret was accepted")
	}
}
