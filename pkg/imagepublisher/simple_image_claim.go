package imagepublisher

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

// Critical claim names are case-sensitive and closed. Optional annotations
// cannot replace them. Duplicate members, including escaped spellings, refuse
// instead of relying on one JSON implementation's last-member-wins behavior.
func validSimpleImageClaim(payload []byte, digest string) bool {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if !uniqueJSONValue(decoder, 0) {
		return false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return false
	}
	claim, ok := simpleClaimObject(payload, "critical", "optional")
	if !ok {
		return false
	}
	critical, ok := simpleClaimObject(claim["critical"], "identity", "image", "type")
	if !ok || len(critical) != 3 || simpleClaimString(critical["type"]) != "cosign container image signature" {
		return false
	}
	identity, ok := simpleClaimObject(critical["identity"], "docker-reference")
	if !ok || simpleClaimString(identity["docker-reference"]) == "" {
		return false
	}
	image, ok := simpleClaimObject(critical["image"], "docker-manifest-digest")
	if !ok || simpleClaimString(image["docker-manifest-digest"]) != digest {
		return false
	}
	if optional, present := claim["optional"]; present && string(optional) != "null" {
		var annotations map[string]json.RawMessage
		if err := json.Unmarshal(optional, &annotations); err != nil || annotations == nil {
			return false
		}
	}
	return true
}

func simpleClaimObject(raw []byte, names ...string) (map[string]json.RawMessage, bool) {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, false
	}
	for key := range object {
		allowed := false
		for _, name := range names {
			allowed = allowed || key == name
		}
		if !allowed {
			return nil, false
		}
	}
	return object, true
}

func simpleClaimString(raw []byte) string {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

func uniqueJSONValue(decoder *json.Decoder, depth int) bool {
	if depth > api.ImageSignatureMaxJSONDepth {
		return false
	}
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delim, container := token.(json.Delim)
	if !container {
		return true
	}
	if delim != '{' && delim != '[' {
		return false
	}
	seen := map[string]bool{}
	for decoder.More() {
		if delim == '{' {
			key, err := decoder.Token()
			name, ok := key.(string)
			if err != nil || !ok || seen[name] {
				return false
			}
			seen[name] = true
		}
		if !uniqueJSONValue(decoder, depth+1) {
			return false
		}
	}
	end, err := decoder.Token()
	return err == nil && ((delim == '{' && end == json.Delim('}')) || (delim == '[' && end == json.Delim(']')))
}
