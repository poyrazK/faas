package httpkit

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// updateAppWireRequest is the PATCH /v1/apps/{slug} body as clients send it.
// UpdateAppRequest.StartCommand had an empty JSON tag until #4488, so every
// CLI built before it sends "StartCommand": null on each app update. Release
// N must keep serving N-1 clients (docs/release-compatibility.md), so the key
// is accepted while it carries no value. The field is internal: a value is
// still rejected and never reaches UpdateAppRequest.StartCommand.
type updateAppWireRequest struct {
	api.UpdateAppRequest
	LegacyStartCommand json.RawMessage `json:"StartCommand,omitempty"`
}

// DecodeUpdateAppRequest decodes an app settings update with DecodeJSON's
// size cap and unknown-field rejection.
func DecodeUpdateAppRequest(r *http.Request) (api.UpdateAppRequest, error) {
	var wire updateAppWireRequest
	if err := DecodeJSON(r, &wire); err != nil {
		return api.UpdateAppRequest{}, err
	}
	if legacy := bytes.TrimSpace(wire.LegacyStartCommand); len(legacy) > 0 && !bytes.Equal(legacy, []byte("null")) {
		return api.UpdateAppRequest{}, errors.New(`json: field "StartCommand" is not an app setting`)
	}
	return wire.UpdateAppRequest, nil
}
