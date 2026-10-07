package objectstorage

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 563
func TestS3ObjectLockStrictConfigurationRead(t *testing.T) {
	const enabled = `<ObjectLockEnabled>Enabled</ObjectLockEnabled>`
	for _, tc := range []struct {
		name, body string
		want       error
		enabled    bool
	}{
		{"empty_success_is_unknown", `<ObjectLockConfiguration/>`, ErrUnavailable, false},
		{"enabled", `<ObjectLockConfiguration>` + enabled + `</ObjectLockConfiguration>`, nil, true},
		{"namespaced", `<ObjectLockConfiguration xmlns="` + objectLockXMLNamespace + `">` + enabled + `</ObjectLockConfiguration>`, nil, true},
		{"prefixed", `<s:ObjectLockConfiguration xmlns:s="` + objectLockXMLNamespace + `"><s:ObjectLockEnabled>Enabled</s:ObjectLockEnabled></s:ObjectLockConfiguration>`, nil, true},
		{"comment", `<ObjectLockConfiguration>` + enabled + `<!-- DefaultRetention --></ObjectLockConfiguration>`, nil, true},
		{"unknown", `<ObjectLockConfiguration>` + enabled + `<FutureProtection>ON</FutureProtection></ObjectLockConfiguration>`, ErrUnsupported, true},
		{"unknown_nested", `<ObjectLockConfiguration><FutureProtection><Hold>ON</Hold></FutureProtection>` + enabled + `</ObjectLockConfiguration>`, ErrUnsupported, true},
		{"unknown_before_enabled", `<ObjectLockConfiguration><FutureProtection>ON</FutureProtection>` + enabled + `</ObjectLockConfiguration>`, ErrUnsupported, true},
		{"wrong_field_namespace", `<ObjectLockConfiguration xmlns="` + objectLockXMLNamespace + `">` + enabled + `<Rule xmlns="urn:foreign"/></ObjectLockConfiguration>`, ErrUnsupported, true},
		{"unknown_attribute", `<ObjectLockConfiguration future="true">` + enabled + `</ObjectLockConfiguration>`, ErrUnsupported, true},
		{"disabled", `<ObjectLockConfiguration><ObjectLockEnabled>Disabled</ObjectLockEnabled></ObjectLockConfiguration>`, ErrUnavailable, false},
		{"empty_enabled", `<ObjectLockConfiguration><ObjectLockEnabled/></ObjectLockConfiguration>`, ErrUnavailable, false},
		{"duplicate", `<ObjectLockConfiguration>` + enabled + enabled + `</ObjectLockConfiguration>`, ErrUnavailable, true},
		{"duplicate_default", `<ObjectLockConfiguration>` + enabled + `<Rule/><Rule/></ObjectLockConfiguration>`, ErrUnavailable, true},
		{"empty_rule", `<ObjectLockConfiguration>` + enabled + `<Rule/></ObjectLockConfiguration>`, ErrUnavailable, true},
		{"empty_default", `<ObjectLockConfiguration>` + enabled + `<Rule><DefaultRetention/></Rule></ObjectLockConfiguration>`, ErrUnavailable, true},
		{"default_without_enabled", `<ObjectLockConfiguration><Rule><DefaultRetention><Mode>GOVERNANCE</Mode><Days>1</Days></DefaultRetention></Rule></ObjectLockConfiguration>`, ErrUnavailable, false},
		{"empty_rule_without_enabled", `<ObjectLockConfiguration><Rule/></ObjectLockConfiguration>`, ErrUnavailable, false},
		{"two_periods", `<ObjectLockConfiguration>` + enabled + `<Rule><DefaultRetention><Mode>COMPLIANCE</Mode><Days>1</Days><Years>1</Years></DefaultRetention></Rule></ObjectLockConfiguration>`, ErrUnavailable, true},
		{"empty_event_hold", `<ObjectLockConfiguration>` + enabled + `<Rule><DefaultRetention><Mode>COMPLIANCE</Mode><Days>1</Days><DefaultEventHold/></DefaultRetention></Rule></ObjectLockConfiguration>`, ErrUnavailable, true},
		{"zero_period", `<ObjectLockConfiguration>` + enabled + `<Rule><DefaultRetention><Mode>COMPLIANCE</Mode><Days>0</Days></DefaultRetention></Rule></ObjectLockConfiguration>`, ErrUnavailable, true},
		{"error_document", `<Error><Code>AccessDenied</Code></Error>`, ErrUnavailable, false},
		{"trailing_document", `<ObjectLockConfiguration>` + enabled + `</ObjectLockConfiguration><ObjectLockConfiguration/>`, ErrUnavailable, true},
		{"doctype", `<!DOCTYPE ObjectLockConfiguration><ObjectLockConfiguration>` + enabled + `</ObjectLockConfiguration>`, ErrUnavailable, false},
		{"wrong_root_namespace", `<ObjectLockConfiguration xmlns="urn:foreign">` + enabled + `</ObjectLockConfiguration>`, ErrUnavailable, false},
		{"malformed", `<ObjectLockConfiguration>`, ErrUnavailable, false},
		{"mixed_text", `<ObjectLockConfiguration>` + enabled + `stray</ObjectLockConfiguration>`, ErrUnavailable, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, tc.body) })).(BucketObjectLockProvider)
			got, err := p.GetBucketObjectLock(t.Context(), "bucket")
			if !errors.Is(err, tc.want) || got.Enabled != tc.enabled {
				t.Fatal(got, err, tc.want)
			}
			if tc.want != nil && got.DefaultRetention != nil {
				t.Fatal("partial policy escaped", got)
			}
		})
	}
}

// adr: 563
func TestS3ObjectLockStrictVersionPolicies(t *testing.T) {
	for _, tc := range []struct {
		name, subresource, body string
		want                    error
	}{
		{"empty_retention", "retention", `<Retention/>`, nil},
		{"fixed", "retention", `<Retention><Mode>GOVERNANCE</Mode><RetainUntilDate>2028-01-01T00:00:00Z</RetainUntilDate></Retention>`, nil},
		{"event_with_minimum", "retention", `<Retention><Mode>COMPLIANCE</Mode><RetainUntilDate>2028-01-01T00:00:00Z</RetainUntilDate><EventHold>ON</EventHold><EventHoldDuration><Years>1</Years></EventHoldDuration></Retention>`, nil},
		{"empty_mode", "retention", `<Retention><Mode/></Retention>`, ErrUnavailable},
		{"mode_only", "retention", `<Retention><Mode>GOVERNANCE</Mode></Retention>`, ErrUnavailable},
		{"bad_date", "retention", `<Retention><Mode>GOVERNANCE</Mode><RetainUntilDate>tomorrow</RetainUntilDate></Retention>`, ErrUnavailable},
		{"empty_hold", "retention", `<Retention><EventHold/></Retention>`, ErrUnavailable},
		{"empty_duration", "retention", `<Retention><Mode>COMPLIANCE</Mode><RetainUntilDate>2028-01-01T00:00:00Z</RetainUntilDate><EventHold>OFF</EventHold><EventHoldDuration/></Retention>`, ErrUnavailable},
		{"event_missing_duration", "retention", `<Retention><Mode>COMPLIANCE</Mode><EventHold>ON</EventHold></Retention>`, ErrUnavailable},
		{"released_without_date", "retention", `<Retention><Mode>COMPLIANCE</Mode><EventHold>OFF</EventHold></Retention>`, ErrUnavailable},
		{"released_observation", "retention", `<Retention><Mode>COMPLIANCE</Mode><EventHold>OFF</EventHold><EventHoldDuration><Years>1</Years></EventHoldDuration><RetainUntilDate>2028-01-01T00:00:00Z</RetainUntilDate></Retention>`, nil},
		{"unknown", "retention", `<Retention><Mode>GOVERNANCE</Mode><RetainUntilDate>2028-01-01T00:00:00Z</RetainUntilDate><FutureProtection>ON</FutureProtection></Retention>`, ErrUnsupported},
		{"unknown_namespace", "retention", `<Retention><Mode xmlns="urn:foreign">GOVERNANCE</Mode><RetainUntilDate>2028-01-01T00:00:00Z</RetainUntilDate></Retention>`, ErrUnsupported},
		{"duplicate_date", "retention", `<Retention><Mode>GOVERNANCE</Mode><RetainUntilDate>2028-01-01T00:00:00Z</RetainUntilDate><RetainUntilDate>2029-01-01T00:00:00Z</RetainUntilDate></Retention>`, ErrUnavailable},
		{"hold_on", "legal-hold", `<LegalHold><Status>ON</Status></LegalHold>`, nil},
		{"hold_off", "legal-hold", `<LegalHold><Status>OFF</Status></LegalHold>`, nil},
		{"empty_legal_hold", "legal-hold", `<LegalHold/>`, ErrUnavailable},
		{"wrong_status", "legal-hold", `<LegalHold><Status>UNKNOWN</Status></LegalHold>`, ErrUnavailable},
		{"duplicate_status", "legal-hold", `<LegalHold><Status>OFF</Status><Status>ON</Status></LegalHold>`, ErrUnavailable},
		{"unknown_legal_hold", "legal-hold", `<LegalHold><Status>OFF</Status><FutureProtection>ON</FutureProtection></LegalHold>`, ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !r.URL.Query().Has(tc.subresource) {
					t.Error(r.URL)
				}
				_, _ = io.WriteString(w, tc.body)
			})).(ObjectVersionLockProvider)
			var err error
			if tc.subresource == "retention" {
				_, err = p.GetObjectVersionRetention(t.Context(), "bucket", "key", "version")
			} else {
				_, err = p.GetObjectVersionLegalHold(t.Context(), "bucket", "key", "version")
			}
			if !errors.Is(err, tc.want) {
				t.Fatal(err, tc.want)
			}
		})
	}
}

// adr: 563
func TestS3ObjectLockMalformedErrorsCannotEstablishAbsence(t *testing.T) {
	for _, body := range []string{
		`<Error><Code>AccessDenied</Code><Code>ObjectLockConfigurationNotFoundError</Code></Error>`,
		`<Error><Code>ObjectLockConfigurationNotFoundError</Code><Code>AccessDenied</Code></Error>`,
		`<Error><Code>ObjectLockConfigurationNotFoundError</Code><FutureProtection>ON</FutureProtection></Error>`,
		`<Error future="true"><Code>ObjectLockConfigurationNotFoundError</Code></Error>`,
		`<Error><Code>ObjectLockConfigurationNotFoundError</Code></Error><Error/>`,
		`<Error><Code xmlns="urn:foreign">ObjectLockConfigurationNotFoundError</Code></Error>`,
	} {
		p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404); _, _ = io.WriteString(w, body) })).(BucketObjectLockProvider)
		if _, err := p.GetBucketObjectLock(t.Context(), "bucket"); !errors.Is(err, ErrUnavailable) {
			t.Fatal("malformed native error became unlocked configuration", err, body)
		}
	}
}

// adr: 563
func TestS3ObjectLockResponseBoundsAndAcknowledgments(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		header http.Header
	}{
		{"oversized_success", 200, strings.Repeat(" ", int(api.MaxObjectLockBodyBytes)+1), nil},
		{"oversized_error", 500, strings.Repeat(" ", int(api.MaxObjectLockBodyBytes)+1), nil},
		{"embedded_error", 200, `<Error><Code>AccessDenied</Code></Error>`, nil},
		{"unexpected_success", 204, "", nil},
		{"wrong_version", 200, "", http.Header{"X-Amz-Version-Id": []string{"other"}}},
		{"duplicate_version", 200, "", http.Header{"X-Amz-Version-Id": []string{"version", "version"}}},
		{"delete_marker", 200, "", http.Header{"X-Amz-Delete-Marker": []string{"true"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tc.header {
					w.Header()[k] = v
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})).(ObjectVersionLockProvider)
			if err := p.PutObjectVersionLegalHold(t.Context(), "bucket", "key", "version", api.ObjectVersionLegalHold{Status: "ON"}); !errors.Is(err, ErrUnavailable) {
				t.Fatal(err)
			}
		})
	}
	var many strings.Builder
	many.WriteString(`<ObjectLockConfiguration>`)
	for i := range api.MaxObjectLockXMLElements {
		fmt.Fprintf(&many, "<Future%d/>", i)
	}
	many.WriteString(`</ObjectLockConfiguration>`)
	for _, body := range []string{
		`<ObjectLockConfiguration>` + strings.Repeat(`<Future>`, api.MaxObjectLockXMLDepth) + strings.Repeat(`</Future>`, api.MaxObjectLockXMLDepth) + `</ObjectLockConfiguration>`,
		many.String(),
	} {
		if _, err := parseBucketObjectLock([]byte(body)); !errors.Is(err, ErrUnavailable) {
			t.Fatal("XML structure was unbounded", err)
		}
	}
}

type objectLockTestHTTPClient struct {
	response *http.Response
	err      error
}

func (c objectLockTestHTTPClient) Do(*http.Request) (*http.Response, error) { return c.response, c.err }

type objectLockTestBody struct {
	*bytes.Reader
	closed bool
}

func (b *objectLockTestBody) Close() error { b.closed = true; return nil }

// adr: 563
func TestS3ObjectLockResponseResources(t *testing.T) {
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, response := range []*http.Response{nil, {StatusCode: 200}} {
		client := objectLockResponseClient{base: objectLockTestHTTPClient{response: response}}
		received, callErr := client.Do(request)
		if received != nil {
			if received.Body != nil {
				_ = received.Body.Close()
			}
			t.Fatal("unexpected policy response", callErr)
		}
		if !errors.Is(callErr, ErrUnavailable) {
			t.Fatal("nil response/body accepted", callErr)
		}
	}
	for _, status := range []int{200, 500} {
		body := &objectLockTestBody{Reader: bytes.NewReader(bytes.Repeat([]byte("x"), int(api.MaxObjectLockBodyBytes)+1))}
		client := objectLockResponseClient{base: objectLockTestHTTPClient{response: &http.Response{StatusCode: status, Body: body}}}
		received, callErr := client.Do(request)
		if received != nil {
			if received.Body != nil {
				_ = received.Body.Close()
			}
			t.Fatal("unexpected oversized policy response", callErr)
		}
		if !errors.Is(callErr, ErrUnavailable) || !body.closed {
			t.Fatal("bounded response leaked body", callErr, body.closed)
		}
		if body.Len() != 0 {
			t.Fatal("response bound was not exact", body.Len())
		}
	}
}
