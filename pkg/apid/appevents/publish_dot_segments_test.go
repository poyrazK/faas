package appevents_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appevents"
)

// `billing.` was accepted and matched the subscription `billing.*` (prod hunt
// r82).
func TestNormalizePublishRequestRefusesEmptyDotSegments(t *testing.T) {
	t.Parallel()
	account := uuid.NewString()
	for _, tc := range []struct {
		source, typ string
		ok          bool
	}{
		{"billing", "invoice.paid", true},
		{"billing.eu", "invoice.paid", true},
		{"billing.", "invoice.paid", false},
		{"billing..x", "invoice.paid", false},
		{".billing", "invoice.paid", false},
		{"billing", "invoice.", false},
		{"billing", "invoice..paid", false},
	} {
		_, err := appevents.NormalizePublishRequest(api.PublishEventRequest{ID: "e1", Source: tc.source, Type: tc.typ, Data: json.RawMessage(`{}`)}, account)
		if (err == nil) != tc.ok {
			t.Errorf("source %q type %q: err = %v, want ok=%v", tc.source, tc.typ, err, tc.ok)
		}
	}
}
