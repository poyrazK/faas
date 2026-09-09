package wire_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/wire"
)

func TestClientIPHeaderStable(t *testing.T) {
	if got := wire.ClientIPHeader; got != "x-faas-client-ip" {
		t.Fatalf("ClientIPHeader = %q, want x-faas-client-ip", got)
	}
}
