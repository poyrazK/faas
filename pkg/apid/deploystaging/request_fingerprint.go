package deploystaging

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"hash"
	"io"
	"mime/multipart"
	"net/url"
	"sort"
)

// RequestFingerprint identifies the semantics of one deployment request, so a
// reused Deployment-Attempt-ID cannot replay the deployment of a different
// request (ADR-20261021101500). Multipart requests are canonical by field name
// and content: the boundary, part order and file names do not count.
type RequestFingerprint struct {
	route string
	query string
	parts []string
	open  []*FingerprintedPart
}

// NewRequestFingerprint starts a fingerprint for a route pattern and its query.
func NewRequestFingerprint(route string, query url.Values) *RequestFingerprint {
	return &RequestFingerprint{route: route, query: query.Encode()}
}

// FingerprintedPart hashes a multipart part as the handler reads it. Bytes the
// handler never reads (beyond a field's size limit) are not part of the request
// it acted on, so they do not count.
type FingerprintedPart struct {
	*multipart.Part
	name string
	sum  hash.Hash
}

func (p *FingerprintedPart) Read(b []byte) (int, error) {
	n, err := p.Part.Read(b)
	p.sum.Write(b[:n])
	return n, err
}

// Track wraps a part so its content joins the fingerprint.
func (f *RequestFingerprint) Track(part *multipart.Part) *FingerprintedPart {
	tracked := &FingerprintedPart{Part: part, name: part.FormName(), sum: sha256.New()}
	f.open = append(f.open, tracked)
	return tracked
}

// AddPart adds a fully read field, such as a parsed form value or file.
func (f *RequestFingerprint) AddPart(name string, content io.Reader) error {
	sum := sha256.New()
	if _, err := io.Copy(sum, content); err != nil {
		return err
	}
	f.parts = append(f.parts, name+"\x00"+hex.EncodeToString(sum.Sum(nil)))
	return nil
}

// AddMultipartForm adds every value and file of a parsed multipart form.
func (f *RequestFingerprint) AddMultipartForm(form *multipart.Form) error {
	if form == nil {
		return nil
	}
	for name, values := range form.Value {
		for _, value := range values {
			if err := f.AddPart(name, bytes.NewReader([]byte(value))); err != nil {
				return err
			}
		}
	}
	for name, files := range form.File {
		for _, header := range files {
			file, err := header.Open()
			if err != nil {
				return err
			}
			err = f.AddPart(name, file)
			_ = file.Close()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// Sum returns the hex SHA-256 fingerprint of everything added so far.
func (f *RequestFingerprint) Sum() string {
	parts := append([]string(nil), f.parts...)
	for _, part := range f.open {
		parts = append(parts, part.name+"\x00"+hex.EncodeToString(part.sum.Sum(nil)))
	}
	sort.Strings(parts)
	sum := sha256.New()
	_, _ = io.WriteString(sum, "deployment-request-v1\x00"+f.route+"\x00"+f.query)
	for _, part := range parts {
		_, _ = io.WriteString(sum, "\x00"+part)
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// JSONRequestFingerprint fingerprints a JSON body by its canonical form: key
// order and insignificant whitespace do not count. ok is false for a body that
// is not one JSON value; the handler reports that request as invalid.
func JSONRequestFingerprint(route string, query url.Values, body []byte) (fingerprint string, ok bool) {
	canonical := []byte("null")
	if len(bytes.TrimSpace(body)) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		var value any
		if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
			return "", false
		}
		var err error
		if canonical, err = json.Marshal(value); err != nil {
			return "", false
		}
	}
	f := NewRequestFingerprint(route, query)
	_ = f.AddPart("body", bytes.NewReader(canonical))
	return f.Sum(), true
}
