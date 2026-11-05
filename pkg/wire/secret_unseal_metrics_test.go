package wire_test

import (
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestRegisterDefaultOpsCountsUnseals pins ADR 20261024014500: once a daemon
// registers its ops metrics, every secretbox decryption in the process lands
// in faas_secret_unseal_total by key and result.
func TestRegisterDefaultOpsCountsUnseals(t *testing.T) {
	ops := wire.NewOpsMetrics("vmmd")
	wire.RegisterDefaultOps(ops)
	t.Cleanup(func() { wire.RegisterDefaultOps(nil) })

	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	blob, err := secretbox.Seal(id.Recipient(), secretbox.Envelope{"K": "v"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secretbox.Open(id, blob); err != nil {
		t.Fatal(err)
	}
	if _, err := secretbox.Open(other, blob); err == nil {
		t.Fatal("the wrong key opened the envelope")
	}

	got := map[[2]string]float64{}
	families, err := ops.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range families {
		if mf.GetName() != "faas_secret_unseal_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			var key [2]string
			for _, l := range m.GetLabel() {
				switch l.GetName() {
				case "identity":
					key[0] = l.GetValue()
				case "result":
					key[1] = l.GetValue()
				}
			}
			got[key] = m.GetCounter().GetValue()
		}
	}
	want := map[[2]string]float64{
		{secretbox.UnsealIdentityUnknown, "ok"}:  1,
		{secretbox.UnsealIdentityNone, "failed"}: 1,
	}
	if len(got) != len(want) {
		t.Fatalf("faas_secret_unseal_total = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("faas_secret_unseal_total = %v, want %v", got, want)
		}
	}
}
