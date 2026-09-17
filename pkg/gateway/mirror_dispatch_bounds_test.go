// adr: 125 — mirror response snapshots must stay bounded at the gateway.

package gateway

import (
	"strings"
	"testing"
)

func TestReadMirrorResponseBodyIsBounded(t *testing.T) {
	body, err := readMirrorResponseBody(strings.NewReader(strings.Repeat("x", int(mirrorResponseBodyCap)+1)))
	if err != nil {
		t.Fatalf("readMirrorResponseBody: %v", err)
	}
	if got := int64(len(body)); got != mirrorResponseBodyCap {
		t.Fatalf("mirror response bytes = %d, want cap %d", got, mirrorResponseBodyCap)
	}
}
