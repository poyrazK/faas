package objectstorage

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestS3BucketVersioningProtocol(t *testing.T) {
	var calls atomic.Int32
	p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !r.URL.Query().Has("versioning") {
			t.Error(r.URL)
		}
		w.Header().Set("Content-Type", "application/xml")
		if r.Method == http.MethodPut {
			var in struct {
				Status string
				MFA    string `xml:"MfaDelete"`
			}
			if xml.NewDecoder(r.Body).Decode(&in) != nil || in.Status != "Suspended" || in.MFA != "" {
				t.Error(in)
			}
			return
		}
		_, _ = fmt.Fprint(w, `<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Status>Enabled</Status><MfaDelete>Disabled</MfaDelete></VersioningConfiguration>`)
	})).(BucketVersioningProvider)
	v, err := p.GetBucketVersioning(t.Context(), "physical")
	if err != nil || v.Status != "Enabled" || v.MFADelete != "Disabled" {
		t.Fatal(v, err)
	}
	if err = p.PutBucketVersioning(t.Context(), "physical", "Suspended"); err != nil {
		t.Fatal(err)
	}
	if err = p.PutBucketVersioning(t.Context(), "physical", ""); !errors.Is(err, ErrInvalid) || calls.Load() != 2 {
		t.Fatal(err, calls.Load())
	}
}
func TestS3BucketVersioningRejectsMalformedAndSingleAttempt(t *testing.T) {
	for _, tc := range []struct {
		body string
		code int
		want error
	}{
		{`<VersioningConfiguration/>`, 200, nil},
		{`<Error><Code>InternalError</Code></Error>`, 200, ErrUnavailable},
		{`<VersioningConfiguration><Status>Enabled</Status><Status>Suspended</Status></VersioningConfiguration>`, 200, ErrUnavailable},
		{`<VersioningConfiguration><Status>Other</Status></VersioningConfiguration>`, 200, ErrUnavailable},
		{`<VersioningConfiguration><Unknown/></VersioningConfiguration>`, 200, ErrUnavailable},
		{`<VersioningConfiguration/><VersioningConfiguration/>`, 200, ErrUnavailable},
		{`<VersioningConfiguration><Status>`, 200, ErrUnavailable},
		{strings.Repeat(" ", 17<<10), 200, ErrUnavailable},
		{`<Error><Code>NotImplemented</Code><Message>private-provider</Message></Error>`, 501, ErrUnsupported},
		{`<Error><Code>InternalError</Code><Message>private-provider</Message></Error>`, 500, ErrUnavailable},
	} {
		t.Run(fmt.Sprint(tc.code, len(tc.body)), func(t *testing.T) {
			var calls atomic.Int32
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.code)
				_, _ = io.WriteString(w, tc.body)
			})).(BucketVersioningProvider)
			_, err := p.GetBucketVersioning(t.Context(), "physical")
			if !errors.Is(err, tc.want) || calls.Load() != 1 || err != nil && strings.Contains(err.Error(), "private-provider") {
				t.Fatal(err, calls.Load())
			}
		})
	}
	var calls atomic.Int32
	p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(500) })).(BucketVersioningProvider)
	if err := p.PutBucketVersioning(t.Context(), "physical", "Enabled"); !errors.Is(err, ErrUnavailable) || calls.Load() != 1 {
		t.Fatal(err, calls.Load())
	}
}
