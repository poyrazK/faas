package productcontrol

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProductPilotHelpHashContract(t *testing.T) {
	value := api.ProductHelpRequest{Request: api.ProductHelpCreate{Message: "customer\nπ <>"}, Response: "owner\r\nreply", Context: api.ProductHelpContext{ObservedAt: time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC), Revision: "terms", PolicyRevision: "policy", Period: "2026-10", Plan: "starter", Reason: "workspace_reported"}}
	message, response, context, err := PilotHelpHashes(value)
	if err != nil || message != "bf8391678510d2fa07b10b321087108f43e472fc2569a0613b5fb7300d1653a6" || response != "19b5f26d7de70cfc021d9e914858b32b19007155ca4ccf0ccbab95db9c620bb9" || context != "422a831e4cdef3009e62d26e28bc935f7fabe1b570f13e8569300791e37a5e51" {
		t.Fatalf("help hash contract changed: %s %s %s %v", message, response, context, err)
	}
}
