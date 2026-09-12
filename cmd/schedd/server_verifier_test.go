package main

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/wire"
)

func TestScheddServerVerifierAcceptsRegisteredNodesAndServiceClients(t *testing.T) {
	registered := wire.NewInmemNodeVerifier()
	registered.Set([]string{"fsn-2.faas"})
	verifier := scheddServerVerifier(registered)

	for _, cn := range []string{
		"fsn-2.faas",
		"gatewayd.faas",
		"meterd.faas",
		"vmmd.faas",
	} {
		if err := verifier.LookupCN(cn); err != nil {
			t.Errorf("LookupCN(%q) = %v, want nil", cn, err)
		}
	}
	if err := verifier.LookupCN("unknown.faas"); !errors.Is(err, wire.ErrNodeVerifierCNMismatch) {
		t.Fatalf("LookupCN(unknown.faas) = %v, want ErrNodeVerifierCNMismatch", err)
	}
}

func TestScheddServerVerifierDisabledWithoutMultiBoxRegistry(t *testing.T) {
	if got := scheddServerVerifier(nil); got != nil {
		t.Fatalf("scheddServerVerifier(nil) = %T, want nil", got)
	}
}
