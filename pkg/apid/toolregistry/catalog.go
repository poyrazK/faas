package toolregistry

import (
	"bytes"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func validCatalog(raw json.RawMessage) bool {
	catalog, err := mcphosting.ReadContract(bytes.NewReader(raw))
	if err != nil || catalog.Version != 2 {
		return false
	}
	canonical, err := mcphosting.MarshalContract(catalog)
	return err == nil && bytes.Equal(raw, canonical)
}
