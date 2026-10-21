package deploystaging

// adr: 20261021101500

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/url"
	"testing"
)

func TestJSONRequestFingerprintIsCanonical(t *testing.T) {
	route := "POST /v1/apps/{slug}/deployments"
	a, okA := JSONRequestFingerprint(route, nil, []byte(`{"image":"r/x@sha256:1","traffic_percent":10}`))
	b, okB := JSONRequestFingerprint(route, nil, []byte("{ \"traffic_percent\": 10,\n \"image\": \"r/x@sha256:1\" }"))
	if !okA || !okB || a != b {
		t.Fatalf("key order or whitespace changed the fingerprint: %s %s", a, b)
	}
	for name, other := range map[string]func() (string, bool){
		"value": func() (string, bool) {
			return JSONRequestFingerprint(route, nil, []byte(`{"image":"r/x@sha256:2","traffic_percent":10}`))
		},
		"route": func() (string, bool) {
			return JSONRequestFingerprint("POST /v1/apps/{slug}/deployments/source-ref", nil, []byte(`{"image":"r/x@sha256:1","traffic_percent":10}`))
		},
		"query": func() (string, bool) {
			return JSONRequestFingerprint(route, url.Values{"dry_run": {"true"}}, []byte(`{"image":"r/x@sha256:1","traffic_percent":10}`))
		},
	} {
		if got, ok := other(); !ok || got == a {
			t.Errorf("%s change kept the fingerprint", name)
		}
	}
	if _, ok := JSONRequestFingerprint(route, nil, []byte(`{"image":`)); ok {
		t.Error("malformed JSON was fingerprinted")
	}
	if _, ok := JSONRequestFingerprint(route, nil, []byte(`{} {}`)); ok {
		t.Error("two JSON values were fingerprinted")
	}
}

type field struct{ name, file, value string }

func multipartBody(t *testing.T, boundary string, fields ...field) (*multipart.Reader, *multipart.Form) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.SetBoundary(boundary); err != nil {
		t.Fatal(err)
	}
	for _, f := range fields {
		var part io.Writer
		var err error
		if f.file != "" {
			part, err = w.CreateFormFile(f.name, f.file)
		} else {
			part, err = w.CreateFormField(f.name)
		}
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(part, f.value)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	form, err := multipart.NewReader(bytes.NewReader(body.Bytes()), boundary).ReadForm(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	return multipart.NewReader(bytes.NewReader(body.Bytes()), boundary), form
}

// streamed reads every part through Track, as the multipart handler does.
func streamed(t *testing.T, reader *multipart.Reader) string {
	t.Helper()
	f := NewRequestFingerprint("POST /v1/apps/{slug}/deployments", nil)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return f.Sum()
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, f.Track(part)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMultipartRequestFingerprintIgnoresTransportDetails(t *testing.T) {
	source := field{"source", "a.tar.gz", "archive-bytes"}
	runtime := field{"runtime", "", "node22"}
	first, form := multipartBody(t, "boundary-one", source, runtime)
	second, _ := multipartBody(t, "boundary-two", runtime, field{"source", "renamed.tar.gz", "archive-bytes"})
	changed, _ := multipartBody(t, "boundary-one", field{"source", "a.tar.gz", "other-bytes"}, runtime)
	a, b, c := streamed(t, first), streamed(t, second), streamed(t, changed)
	if a != b {
		t.Fatalf("boundary, part order or file name changed the fingerprint")
	}
	if a == c {
		t.Fatal("source content did not change the fingerprint")
	}
	parsed := NewRequestFingerprint("POST /v1/apps/{slug}/deployments", nil)
	if err := parsed.AddMultipartForm(form); err != nil {
		t.Fatal(err)
	}
	if parsed.Sum() != a {
		t.Fatal("a parsed form and a streamed body of the same fields disagree")
	}
}
