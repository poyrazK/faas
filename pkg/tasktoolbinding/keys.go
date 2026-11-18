package tasktoolbinding

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"regexp"
)

func OwnerKeysMatch(a, b string) bool {
	if len(a) < api.HostedTaskBindingOwnerKeyMinBytes || len(b) < api.HostedTaskBindingOwnerKeyMinBytes {
		return false
	}
	x, y := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(x[:], y[:]) == 1
}

type bindingPayloadKeys struct {
	ActiveKeyID string            `json:"activeKeyId"`
	Keys        map[string]string `json:"keys"`
}

var bindingPayloadKeyID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func parseBindingPayloadKeys(s string) (bindingPayloadKeys, bool) {
	var out bindingPayloadKeys
	decoder := json.NewDecoder(bytes.NewBufferString(s))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&out) != nil || !bindingPayloadKeyID.MatchString(out.ActiveKeyID) || len(out.Keys) < 1 || len(out.Keys) > api.HostedTaskBindingPreviousKeysMax {
		return out, false
	}
	// Reject trailing JSON, matching JSON.parse.
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return out, false
	}
	if out.ActiveKeyID != "legacy" && out.Keys[out.ActiveKeyID] == "" {
		return out, false
	}
	for id, key := range out.Keys {
		if id == "legacy" || !bindingPayloadKeyID.MatchString(id) || len(key) < api.HostedTaskBindingOwnerKeyMinBytes {
			return out, false
		}
	}
	return out, true
}
func PayloadKeysMatch(a, b string) bool {
	x, ok := parseBindingPayloadKeys(a)
	y, valid := parseBindingPayloadKeys(b)
	if !ok || !valid || x.ActiveKeyID != y.ActiveKeyID || len(x.Keys) != len(y.Keys) {
		return false
	}
	for id, key := range x.Keys {
		if !OwnerKeysMatch(key, y.Keys[id]) {
			return false
		}
	}
	return true
}
