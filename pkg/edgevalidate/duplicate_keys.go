package edgevalidate

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
)

// duplicateKeyProblem reports the first JSON object, at any depth, that names
// a property twice: exactly, or in two spellings that differ only in case
// (ADR-20261107190000). The schema validator decodes a body with
// exact, last-wins keys, while Go's encoding/json struct binding and ASP.NET
// Core's web defaults bind property names case-insensitively, and some
// parsers keep the first duplicate. So {"role":"user","ROLE":"admin"} passed
// a schema allowing only role=user and reached a Go app as role=admin. A
// body that is not JSON is left to the schema check, which refuses it.
func duplicateKeyProblem(body []byte) *FieldError {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	fe, err := walkDuplicateKeys(dec, "")
	if err != nil {
		return nil
	}
	return fe
}

// walkDuplicateKeys reads one JSON value from dec. path is its JSON Pointer.
func walkDuplicateKeys(dec *json.Decoder, path string) (*FieldError, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil, nil
	}
	switch delim {
	case '{':
		seen := map[string]string{}
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, _ := keyTok.(string)
			folded := strings.ToLower(key)
			if first, dup := seen[folded]; dup {
				got := "duplicate property " + strconv.Quote(key)
				if first != key {
					got = "properties " + strconv.Quote(first) + " and " + strconv.Quote(key) + " differ only in case"
				}
				return &FieldError{Field: path + "/" + escapePointer(key), Expected: "unique_properties", Got: got}, nil
			}
			seen[folded] = key
			if fe, err := walkDuplicateKeys(dec, path+"/"+escapePointer(key)); fe != nil || err != nil {
				return fe, err
			}
		}
	case '[':
		for i := 0; dec.More(); i++ {
			if fe, err := walkDuplicateKeys(dec, path+"/"+strconv.Itoa(i)); fe != nil || err != nil {
				return fe, err
			}
		}
	default:
		return nil, errors.New("edgevalidate: unexpected delimiter")
	}
	if _, err := dec.Token(); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return nil, nil
}

// escapePointer escapes one JSON Pointer reference token (RFC 6901).
func escapePointer(token string) string {
	return strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
}
