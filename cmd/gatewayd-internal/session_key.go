// gatewayd-internal holds no session key (ADR-20261102191500): apid opens
// session cookies for it, and the gateway fleet key signs what gateways
// check of one another. ADR-570 derives the managed-deadline MAC key from
// that master.
package main

import (
	"github.com/onebox-faas/faas/pkg/trafficdeadline"
)

// newTrafficDeadlineSigner signs managed deadlines under the gateway fleet
// key, which every gateway shares: the gateway on the guest's node checks a
// token another gateway signed.
func newTrafficDeadlineSigner(fleetKey []byte) (*trafficdeadline.Signer, error) {
	return trafficdeadline.New(fleetKey, nil)
}
