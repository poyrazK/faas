package api_test

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestApplicationStandardPublicationRequiresUnambiguousVersion(t *testing.T) {
	for _, raw := range []string{
		`{"definition":{"require_signed":{"mode":"mandatory","value":true}}}`,
		`{"expected_version":null,"definition":{}}`,
		`{"expected_version":0,"expected_version":1,"definition":{}}`,
		`{"expected_version":0,"definition":{},"activate":true}`,
	} {
		var request api.CreateApplicationStandardVersionRequest
		if err := json.Unmarshal([]byte(raw), &request); err == nil {
			t.Fatalf("accepted ambiguous publication %s", raw)
		}
	}
	var request api.CreateApplicationStandardVersionRequest
	if err := json.Unmarshal([]byte(`{"expected_version":0,"definition":{"require_signed":{"mode":"mandatory","value":true}}}`), &request); err != nil {
		t.Fatal(err)
	}
}
