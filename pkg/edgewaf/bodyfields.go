package edgewaf

import (
	"bytes"
	"encoding/json"
	"mime"
	"strings"
)

// boundBodyFields cuts a body prefix after its first n fields, so an
// in-path check scores at most n parsed arguments (ADR-831 amendment 22):
// CRS runs each body rule once per argument, so the check's cost grows with
// fields, not bytes. JSON is cut after n leaf values, a URL-encoded form
// after n pairs; the cut falls on a token boundary, and CRS scores a cut-off
// prefix as it is (SecRequestBodyLimitAction ProcessPartial). Other types
// are returned whole: CRS parses them as one argument. It reports whether it
// cut.
func boundBodyFields(body []byte, contentType string, n int) ([]byte, bool) {
	if len(body) == 0 || n <= 0 {
		return body, false
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return body, false
	}
	switch {
	case mediaType == "application/json" || (strings.HasPrefix(mediaType, "application/") && strings.HasSuffix(mediaType, "+json")):
		return boundJSONFields(body, n)
	case mediaType == "application/x-www-form-urlencoded":
		return boundFormFields(body, n)
	}
	return body, false
}

// boundFormFields cuts a URL-encoded body before its (n+1)th pair.
func boundFormFields(body []byte, n int) ([]byte, bool) {
	at := 0
	for range n {
		i := bytes.IndexByte(body[at:], '&')
		if i < 0 {
			return body, false
		}
		at += i + 1
	}
	return body[:at-1], true
}

// boundJSONFields cuts a JSON body before its (n+1)th leaf value (string,
// number, bool or null that is not an object key). A body that is not
// valid JSON up to that point is returned whole, for the engine to treat as
// it would without the bound.
func boundJSONFields(body []byte, n int) ([]byte, bool) {
	type frame struct{ object, wantKey bool }
	var stack []frame
	// value marks that the enclosing object's pending value was consumed.
	value := func() {
		if top := len(stack) - 1; top >= 0 && stack[top].object {
			stack[top].wantKey = true
		}
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	leaves := 0
	// keyAt is where the current object member's key starts: a cut before
	// a member's value drops its key too.
	keyAt := int64(0)
	for {
		before := dec.InputOffset()
		tok, err := dec.Token()
		if err != nil {
			return body, false
		}
		inObject := len(stack) > 0 && stack[len(stack)-1].object
		if d, ok := tok.(json.Delim); ok {
			switch d {
			case '{', '[':
				value()
				stack = append(stack, frame{object: d == '{', wantKey: d == '{'})
			case '}', ']':
				stack = stack[:len(stack)-1]
			}
			continue
		}
		if inObject && stack[len(stack)-1].wantKey {
			stack[len(stack)-1].wantKey = false
			keyAt = before
			continue
		}
		if leaves == n {
			if inObject {
				before = keyAt
			}
			return body[:before], true
		}
		leaves++
		value()
	}
}
