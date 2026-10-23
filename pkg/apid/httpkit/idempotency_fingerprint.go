package httpkit

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/state"
)

// IdempotencyFingerprintMaxBodyBytes is the largest body a request
// fingerprint covers (ADR-20261022204036). A larger body, such as a source
// upload, is not buffered: its key stays unbound and replays as before.
const IdempotencyFingerprintMaxBodyBytes = DefaultJSONBodyMaxBytes

// idempotencyFingerprintHeaders change what a request means without changing
// its body. Every other header is transport detail a retry may vary.
var idempotencyFingerprintHeaders = []string{"If-Match", "If-None-Match", "If-Workload-Revision"}

// IdempotencyFingerprint returns the keyed request fingerprint that an
// Idempotency-Key is bound to (ADR-20261022204036). It is a key id followed
// by a truncated HMAC-SHA256 over the method, path, canonical query, the
// Content-Type media type, the precondition headers and the canonical body.
// Equivalent JSON bodies, multipart bodies that differ only in their
// boundary, and queries that differ only in order or in naming the default
// environment get one fingerprint. The MAC is keyed so a stored fingerprint
// cannot be brute-forced back to a low-entropy secret carried in the body.
// The MAC key is derived from hostKey for this use alone, and the key id
// (state.IdempotencyFingerprintKeyIDSize bytes) names it, so fingerprints
// made under a rotated key are not compared.
//
// It reads the body and replaces r.Body with one that yields the same bytes.
// It returns nil, and the key is not bound, when hostKey is empty or the body
// is larger than IdempotencyFingerprintMaxBodyBytes or could not be read.
func IdempotencyFingerprint(r *http.Request, hostKey []byte) []byte {
	if len(hostKey) == 0 || r.ContentLength > IdempotencyFingerprintMaxBodyBytes {
		return nil
	}
	var body []byte
	if r.Body != nil && r.Body != http.NoBody {
		var err error
		body, err = io.ReadAll(io.LimitReader(r.Body, IdempotencyFingerprintMaxBodyBytes+1))
		r.Body = replayedBody{Reader: io.MultiReader(bytes.NewReader(body), r.Body), Closer: r.Body}
		if err != nil || int64(len(body)) > IdempotencyFingerprintMaxBodyBytes {
			return nil
		}
	}
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		mediaType, params = strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))), nil
	}
	key := hmacSum(hostKey, []byte("gregale idempotency request fingerprint v1"))
	mac := hmac.New(sha256.New, key)
	writeFingerprintField(mac, r.Method)
	writeFingerprintField(mac, r.URL.Path)
	writeFingerprintField(mac, canonicalQuery(r.URL.RawQuery))
	writeFingerprintField(mac, mediaType)
	for _, name := range idempotencyFingerprintHeaders {
		writeFingerprintField(mac, name+":"+strings.Join(r.Header.Values(name), "\x00"))
	}
	kind, canonical := canonicalBody(mediaType, params, body)
	writeFingerprintField(mac, kind)
	writeFingerprintField(mac, string(canonical))
	fingerprint := make([]byte, 0, state.IdempotencyFingerprintSize)
	fingerprint = append(fingerprint, hmacSum(key, []byte("key id"))[:state.IdempotencyFingerprintKeyIDSize]...)
	return append(fingerprint, mac.Sum(nil)[:state.IdempotencyFingerprintSize-state.IdempotencyFingerprintKeyIDSize]...)
}

func hmacSum(key, message []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(message)
	return mac.Sum(nil)
}

// replayedBody hands the handler the bytes the fingerprint read, then the
// rest of the original body, and closes the original.
type replayedBody struct {
	io.Reader
	io.Closer
}

// canonicalQuery sorts the query. An environment selector naming the default
// (environment=production) is the request that omits it.
func canonicalQuery(raw string) string {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return raw
	}
	if env := values["environment"]; len(env) == 1 && env[0] == "production" {
		delete(values, "environment")
	}
	return values.Encode()
}

// writeFingerprintField length-prefixes each field so no two field lists
// share an encoding.
func writeFingerprintField(w io.Writer, field string) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(field)))
	_, _ = w.Write(n[:])
	_, _ = io.WriteString(w, field)
}

// canonicalBody returns the body in a form that is equal for equivalent
// requests: JSON re-encoded with sorted keys, multipart parts without the
// random boundary. Anything else, or a body that does not parse, is its
// bytes.
func canonicalBody(mediaType string, params map[string]string, body []byte) (string, []byte) {
	if len(body) == 0 {
		return "empty", nil
	}
	switch {
	case mediaType == "" || mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
		if canonical, ok := canonicalJSON(body); ok {
			return "json", canonical
		}
	case mediaType == "multipart/form-data" && params["boundary"] != "":
		if canonical, ok := canonicalMultipart(body, params["boundary"]); ok {
			return "multipart", canonical
		}
	}
	return "raw", body
}

func canonicalJSON(body []byte) ([]byte, bool) {
	if !utf8.Valid(body) || !json.Valid(body) {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, false
	}
	return out.Bytes(), true
}

// canonicalMultipart encodes each part's form name, file name, content type
// and raw content, in order.
func canonicalMultipart(body []byte, boundary string) ([]byte, bool) {
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	var out bytes.Buffer
	for {
		part, err := reader.NextRawPart()
		if errors.Is(err, io.EOF) {
			return out.Bytes(), true
		}
		if err != nil {
			return nil, false
		}
		content, err := io.ReadAll(part)
		if err != nil {
			return nil, false
		}
		writeFingerprintField(&out, part.FormName())
		writeFingerprintField(&out, part.FileName())
		writeFingerprintField(&out, part.Header.Get("Content-Type"))
		writeFingerprintField(&out, string(content))
	}
}
