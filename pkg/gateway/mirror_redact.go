// mirror_redact.go — issue #72 / ADR-124 / ADR-125 PR-A3
// mirror-invocation header stripping + result classification.
//
// The mirror goroutine (pkg/gateway/mirror_dispatch.go) builds a
// per-request mirror invocation: same path / method / body as the
// source request, but with a stripped header set so customer auth
// material never reaches the customer's own mirror deployment. The
// always-stripped set covers every header a customer authentication
// surface might leak through; the customer-supplied redact_headers
// list lets a customer opt further fields out (typical use:
// custom `X-Tenant-Secret` they want kept out of the mirror even
// though it's not standard auth material).
//
// Result classification separates HTTP status, JSON shape, response values,
// and mirror failures. Bounded or missing snapshots are marked incomplete
// rather than being reported as equal.

package gateway

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/textproto"
	"sort"

	"github.com/onebox-faas/faas/pkg/state"
)

// alwaysStrippedHeaders (issue #72 / ADR-125 PR-A3) is the
// header set every mirror invocation strips from the source
// request, regardless of customer configuration. The set covers:
//
//   - Authorization / Proxy-Authorization: standard auth headers
//     (Basic, Bearer, ...); leaking them lets the mirror VM
//     impersonate the source caller.
//   - Cookie / Set-Cookie: session material; cookies can carry
//     auth tokens the customer didn't think of as "auth headers".
//   - X-API-Key: per-vendor auth on top of OAuth/JWT. Many
//     platforms (Stripe-style, GitHub-style) use this header.
//   - WWW-Authenticate: server→client auth challenge; safe to
//     keep out of the mirror probe, the mirror VM never sees a
//     401 anyway because it receives the forwarded 2xx/4xx.
//
// Lower-case, matched case-insensitively against http.Header
// keys per Go's canonicalisation (textproto.CanonicalMIMEHeaderKey
// already lowercases the first letter of each dash-delimited word,
// but callers may pass "AUTHORIZATION" verbatim). http.Header.Del
// uses CanonicalMIMEHeaderKey internally, so a plain Del(key)
// after Set-ing the always-stripped names handles both shapes.
var alwaysStrippedHeaders = []string{
	"Authorization",
	"Cookie",
	"Set-Cookie",
	"X-API-Key",
	"Proxy-Authorization",
	"WWW-Authenticate",
}

// StrippedRequestHeaders (issue #72 / ADR-124 / ADR-125 PR-A3)
// returns a fresh http.Header carrying every header from src
// EXCEPT those whose name (case-insensitive) appears in
// alwaysStrippedHeaders or in rule.RedactHeaders. The mirror
// goroutine uses the result as the outgoing request's headers.
//
// The src parameter is NOT mutated — the caller keeps the
// customer's original header set for the source VM round-trip.
// Implementation note: build a map of the stripped names first
// (O(s) where s = |alwaysStrippedHeaders| + |rule.RedactHeaders|)
// so the per-header walk is O(1) lookup. A customer with 1000
// headers on the source request still pays linear time.
func StrippedRequestHeaders(rule state.MirrorRule, src http.Header) http.Header {
	stripped := make(map[string]struct{}, len(alwaysStrippedHeaders)+len(rule.RedactHeaders))
	for _, k := range alwaysStrippedHeaders {
		stripped[textproto.CanonicalMIMEHeaderKey(k)] = struct{}{}
	}
	for _, k := range rule.RedactHeaders {
		stripped[textproto.CanonicalMIMEHeaderKey(k)] = struct{}{}
	}
	dst := make(http.Header, len(src))
	for k, vs := range src {
		if _, drop := stripped[textproto.CanonicalMIMEHeaderKey(k)]; drop {
			continue
		}
		// Copy the slice so a downstream mutation doesn't
		// leak back into the source request.
		cp := make([]string, len(vs))
		copy(cp, vs)
		dst[k] = cp
	}
	return dst
}

func safeMirrorMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

// MirrorComparison is the body-free result of comparing bounded source and
// mirror snapshots. Schema drift is based on JSON structure; body drift is
// based on canonical JSON values (or exact bytes for non-JSON) and only runs
// when enabled. Missing or truncated snapshots are marked incomplete. Hashes
// are fingerprints only; raw bodies are never returned or persisted.
type MirrorComparison struct {
	StatusDiff       bool
	SchemaDiff       bool
	BodyDiff         bool
	Crashed          bool
	Incomplete       bool
	SourceSchemaHash []byte
	MirrorSchemaHash []byte
	SourceBodyHash   []byte
	MirrorBodyHash   []byte
}

// CompareMirrorResponses treats JSON whitespace and object-key order as
// formatting. schema_diff means the JSON shape changed; body_diff means the
// canonical JSON values (or raw non-JSON bytes) changed, and is only computed
// when includeBody is enabled. Truncated or missing snapshots are inconclusive
// for body/schema comparison rather than false matches or false differences.
func CompareMirrorResponses(srcStatus int, srcBody []byte, srcTruncated bool, mirrorStatus int, mirrorBody []byte, mirrorTruncated bool, includeBody bool) MirrorComparison {
	result := MirrorComparison{
		Incomplete: srcStatus == 0 || mirrorStatus == 0 || srcTruncated || mirrorTruncated,
		Crashed:    mirrorStatus == 0 || mirrorStatus >= http.StatusInternalServerError,
	}
	if srcStatus > 0 && mirrorStatus > 0 {
		result.StatusDiff = srcStatus != mirrorStatus
	}
	if result.Incomplete {
		return result
	}
	source := fingerprintResponse(srcBody, includeBody)
	mirror := fingerprintResponse(mirrorBody, includeBody)
	if source.isJSON && mirror.isJSON {
		result.SourceSchemaHash = source.schemaHash
		result.MirrorSchemaHash = mirror.schemaHash
		result.SchemaDiff = !bytes.Equal(source.schemaHash, mirror.schemaHash)
	} else if source.isJSON != mirror.isJSON {
		result.SchemaDiff = true
	}
	if includeBody {
		result.SourceBodyHash = source.bodyHash
		result.MirrorBodyHash = mirror.bodyHash
		result.BodyDiff = !bytes.Equal(source.bodyHash, mirror.bodyHash)
	}
	return result
}

type responseFingerprint struct {
	isJSON     bool
	schemaHash []byte
	bodyHash   []byte
}

func fingerprintResponse(body []byte, includeBody bool) responseFingerprint {
	fingerprint := responseFingerprint{}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		if includeBody {
			fingerprint.bodyHash = hashBytes(body)
		}
		return fingerprint
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if includeBody {
			fingerprint.bodyHash = hashBytes(body)
		}
		return fingerprint
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		if includeBody {
			fingerprint.bodyHash = hashBytes(body)
		}
		return fingerprint
	}
	shape, err := json.Marshal(jsonSchemaShape(value))
	if err != nil {
		if includeBody {
			fingerprint.bodyHash = hashBytes(body)
		}
		return fingerprint
	}
	fingerprint.isJSON = true
	fingerprint.schemaHash = hashBytes(shape)
	if includeBody {
		fingerprint.bodyHash = hashBytes(canonical)
	}
	return fingerprint
}

func hashBytes(body []byte) []byte {
	// codeql[go/weak-sensitive-hashing] These are comparison fingerprints only, never password or credential verifiers; body-value fingerprints are opt-in and raw response bytes are not persisted.
	hash := sha256.Sum256(body)
	return append([]byte(nil), hash[:]...)
}

func jsonSchemaShape(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		properties := make(map[string]any, len(typed))
		for key, property := range typed {
			properties[key] = jsonSchemaShape(property)
		}
		return map[string]any{"type": "object", "properties": properties}
	case []any:
		if len(typed) == 0 {
			return map[string]any{"type": "array"}
		}
		shapes := make(map[string]any, len(typed))
		for _, item := range typed {
			shape := jsonSchemaShape(item)
			encoded, err := json.Marshal(shape)
			if err == nil {
				shapes[string(encoded)] = shape
			}
		}
		keys := make([]string, 0, len(shapes))
		for key := range shapes {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		items := make([]any, 0, len(keys))
		for _, key := range keys {
			items = append(items, shapes[key])
		}
		return map[string]any{"type": "array", "items": items}
	case string:
		return map[string]string{"type": "string"}
	case json.Number:
		return map[string]string{"type": "number"}
	case bool:
		return map[string]string{"type": "boolean"}
	case nil:
		return map[string]string{"type": "null"}
	default:
		return map[string]string{"type": "unknown"}
	}
}

func ClassifyResult(srcStatus int, srcBody []byte, mirrorStatus int, mirrorBody []byte) (statusDiff, schemaDiff, bodyDiff, crashed bool) {
	comparison := CompareMirrorResponses(srcStatus, srcBody, false, mirrorStatus, mirrorBody, false, true)
	return comparison.StatusDiff, comparison.SchemaDiff, comparison.BodyDiff, comparison.Crashed
}

// ClassifyResultWithHashes returns exact raw-body SHA-256 hashes for callers
// that compare against a caller-supplied historical hash. Live mirror
// comparisons use CompareMirrorResponses so schema and value drift remain
// distinct.
func ClassifyResultWithHashes(srcStatus int, srcBody []byte, mirrorStatus int, mirrorBody []byte) (statusDiff, schemaDiff, bodyDiff, crashed bool, srcFingerprint, mirrorFingerprint [sha256.Size]byte) {
	if srcStatus != mirrorStatus {
		statusDiff = true
	}
	srcHash := sha256.Sum256(srcBody)
	mirrorHash := sha256.Sum256(mirrorBody)
	if srcHash != mirrorHash {
		schemaDiff = true
		bodyDiff = true
	}
	if mirrorStatus == 0 || mirrorStatus >= 500 {
		crashed = true
	}
	return statusDiff, schemaDiff, bodyDiff, crashed, srcHash, mirrorHash
}
