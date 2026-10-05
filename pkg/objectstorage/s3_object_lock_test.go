package objectstorage

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 563
func TestS3ObjectLockNativeProtocol(t *testing.T) {
	key, version := "目录/a +?%.txt", "private/+?%version"
	date := time.Date(2028, 1, 1, 0, 0, 0, 123456789, time.FixedZone("offset", 3600))
	day, year := int32(3), int32(2)
	var requests atomic.Int32
	var configuration, retention, hold string
	var mu sync.Mutex
	p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests.Add(1)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Error("unsigned native policy request")
		}
		query := r.URL.Query()
		var doc *string
		switch {
		case query.Has("object-lock"):
			if r.URL.Path != "/bucket" || query.Has("versionId") {
				t.Error("wrong bucket target", r.URL)
			}
			doc = &configuration
		case query.Has("retention"):
			doc = &retention
		case query.Has("legal-hold"):
			doc = &hold
		default:
			t.Error("unexpected subresource", r.URL)
			w.WriteHeader(400)
			return
		}
		if doc != &configuration && (r.URL.Path != "/bucket/"+key || query.Get("versionId") != version) {
			t.Error("exact selector was lost", r.URL)
		}
		if doc != &retention && r.Header.Get("X-Amz-Bypass-Governance-Retention") != "" {
			t.Error("bypass leaked to unrelated operation")
		}
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, *doc)
			return
		}
		if r.Method != http.MethodPut {
			t.Error("unexpected method", r.Method)
		}
		body, err := io.ReadAll(r.Body)
		sum := sha256.Sum256(body)
		if err != nil || r.Header.Get("X-Amz-Checksum-Sha256") != base64.StdEncoding.EncodeToString(sum[:]) || r.Header.Get("X-Amz-Sdk-Checksum-Algorithm") != "SHA256" {
			t.Error("missing exact policy checksum", err, r.Header)
		}
		*doc = string(body)
		if doc == &retention && strings.Contains(*doc, "<EventHold>OFF</EventHold>") && !strings.Contains(*doc, "<RetainUntilDate>") {
			// Release fixes the native date from the previously stored duration.
			*doc = strings.Replace(*doc, "</Retention>", "<RetainUntilDate>2028-01-01T00:00:00Z</RetainUntilDate></Retention>", 1)
		}
	}))
	buckets, versions := p.(BucketObjectLockProvider), p.(ObjectVersionLockProvider)
	for _, c := range []api.ObjectBucketObjectLockConfiguration{
		{Enabled: true, DefaultRetention: &api.ObjectLockDefaultRetention{Mode: "GOVERNANCE", Days: &day}},
		{Enabled: true, DefaultRetention: &api.ObjectLockDefaultRetention{Mode: "COMPLIANCE", Years: &year}},
		{Enabled: true, DefaultRetention: &api.ObjectLockDefaultRetention{Mode: "COMPLIANCE", DefaultEventHold: &api.ObjectRetentionPeriod{Days: &day}}},
		{Enabled: true, DefaultRetention: &api.ObjectLockDefaultRetention{Mode: "GOVERNANCE", Days: &day, DefaultEventHold: &api.ObjectRetentionPeriod{Years: &year}}},
		{Enabled: true},
	} {
		if err := buckets.PutBucketObjectLock(t.Context(), "bucket", c); err != nil {
			t.Fatal(err)
		}
		got, err := buckets.GetBucketObjectLock(t.Context(), "bucket")
		mu.Lock()
		document := configuration
		mu.Unlock()
		if err != nil || !got.Enabled || !got.Valid() {
			t.Fatal(got, err, document)
		}
		if c.DefaultRetention == nil {
			if got.DefaultRetention != nil || strings.Contains(document, "<Rule>") {
				t.Fatal("clear disabled lock or retained default", got, document)
			}
		} else {
			a, _ := json.Marshal(c)
			b, _ := json.Marshal(got)
			if string(a) != string(b) {
				t.Fatal("native default changed", string(a), string(b))
			}
		}
	}
	for _, r := range []api.ObjectVersionRetention{
		{Mode: "COMPLIANCE", RetainUntilDate: &date},
		{Mode: "GOVERNANCE", EventHold: "ON", EventHoldDuration: &api.ObjectRetentionPeriod{Days: &day}},
		{Mode: "COMPLIANCE", RetainUntilDate: &date, EventHold: "ON", EventHoldDuration: &api.ObjectRetentionPeriod{Years: &year}},
		{Mode: "COMPLIANCE", EventHold: "OFF"},
		{},
	} {
		if err := versions.PutObjectVersionRetention(t.Context(), "bucket", key, version, r, false); err != nil {
			t.Fatal(err)
		}
		got, err := versions.GetObjectVersionRetention(t.Context(), "bucket", key, version)
		mu.Lock()
		document := retention
		mu.Unlock()
		if err != nil || got.Mode != r.Mode || got.EventHold != r.EventHold || got.Empty() != r.Empty() {
			t.Fatal(got, err, document)
		}
		if r.RetainUntilDate != nil && (got.RetainUntilDate == nil || !got.RetainUntilDate.Equal(*r.ForWrite().RetainUntilDate)) {
			t.Fatal("native date shortened", got, document)
		}
		if r.EventHoldDuration != nil && (got.EventHoldDuration == nil || !equalObjectLockPeriod(*r.EventHoldDuration, *got.EventHoldDuration)) {
			t.Fatal("event duration lost", got, document)
		}
	}
	for _, status := range []string{"ON", "OFF"} {
		if err := versions.PutObjectVersionLegalHold(t.Context(), "bucket", key, version, api.ObjectVersionLegalHold{Status: status}); err != nil {
			t.Fatal(err)
		}
		got, err := versions.GetObjectVersionLegalHold(t.Context(), "bucket", key, version)
		if err != nil || got.Status != status {
			t.Fatal(got, err)
		}
	}
	if requests.Load() != 24 {
		t.Fatal("unexpected native attempts", requests.Load())
	}
}

func equalObjectLockPeriod(a, b api.ObjectRetentionPeriod) bool {
	return (a.Days == nil && b.Days == nil || a.Days != nil && b.Days != nil && *a.Days == *b.Days) && (a.Years == nil && b.Years == nil || a.Years != nil && b.Years != nil && *a.Years == *b.Years)
}

// adr: 563
func TestS3ObjectLockExactTargetsAndExplicitBypass(t *testing.T) {
	var requests atomic.Int32
	p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if !r.URL.Query().Has("retention") || r.URL.Query().Get("versionId") != "null" {
			t.Error("unbound selector", r.URL)
		}
		want := ""
		if n == 2 {
			want = "true"
		}
		if r.Header.Get("X-Amz-Bypass-Governance-Retention") != want {
			t.Error("implicit/missing bypass", r.Header)
		}
	}))
	v := p.(ObjectVersionLockProvider)
	for _, bypass := range []bool{false, true} {
		if err := v.PutObjectVersionRetention(t.Context(), "bucket", "key", "null", api.ObjectVersionRetention{}, bypass); err != nil {
			t.Fatal(err)
		}
	}
	for _, selector := range []string{"", "bad\nversion", strings.Repeat("v", api.ObjectProviderVersionIDMaxBytes+1)} {
		if _, err := v.GetObjectVersionRetention(t.Context(), "bucket", "key", selector); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
		if _, err := v.GetObjectVersionLegalHold(t.Context(), "bucket", "key", selector); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
		if err := v.PutObjectVersionRetention(t.Context(), "bucket", "key", selector, api.ObjectVersionRetention{}, true); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
		if err := v.PutObjectVersionLegalHold(t.Context(), "bucket", "key", selector, api.ObjectVersionLegalHold{Status: "ON"}); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if err := p.(BucketObjectLockProvider).PutBucketObjectLock(t.Context(), "bucket", api.ObjectBucketObjectLockConfiguration{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("disabled lock accepted", err)
	}
	if requests.Load() != 2 {
		t.Fatal("invalid request reached native provider", requests.Load())
	}
}

// adr: 563
func TestS3ObjectLockMissingConfigurationIsExact(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status int
		want   error
	}{
		{"ObjectLockConfigurationNotFoundError", 404, nil},
		{"ObjectLockConfigurationNotFoundError", 403, ErrUnavailable},
		{"NoSuchBucket", 404, ErrNotFound},
		{"AccessDenied", 403, ErrConfiguration},
		{"NotImplemented", 501, ErrUnsupported},
	} {
		t.Run(tc.code+http.StatusText(tc.status), func(t *testing.T) {
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `<Error><Code>`+tc.code+`</Code></Error>`)
			})).(BucketObjectLockProvider)
			c, err := p.GetBucketObjectLock(t.Context(), "bucket")
			if !errors.Is(err, tc.want) || c.Enabled || c.DefaultRetention != nil {
				t.Fatal(c, err, tc.want)
			}
		})
	}
}

// adr: 563
func TestS3ObjectLockMutationsNeverRetry(t *testing.T) {
	for _, action := range []string{"config", "retention", "hold"} {
		t.Run(action, func(t *testing.T) {
			var requests atomic.Int32
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(500)
				_, _ = io.WriteString(w, `<Error><Code>InternalError</Code></Error>`)
			}))
			var err error
			switch action {
			case "config":
				err = p.(BucketObjectLockProvider).PutBucketObjectLock(t.Context(), "bucket", api.ObjectBucketObjectLockConfiguration{Enabled: true})
			case "retention":
				err = p.(ObjectVersionLockProvider).PutObjectVersionRetention(t.Context(), "bucket", "key", "version", api.ObjectVersionRetention{}, false)
			case "hold":
				err = p.(ObjectVersionLockProvider).PutObjectVersionLegalHold(t.Context(), "bucket", "key", "version", api.ObjectVersionLegalHold{Status: "ON"})
			}
			if !errors.Is(err, ErrUnavailable) || requests.Load() != 1 {
				t.Fatal(err, requests.Load())
			}
		})
	}
}

// adr: 563
func TestS3ObjectLockReadAfterLostAcknowledgment(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		for _, action := range []string{"config", "retention", "hold"} {
			t.Run(action+map[bool]string{false: "_500", true: "_disconnect"}[disconnect], func(t *testing.T) {
				var writes, reads atomic.Int32
				var native string
				var mu sync.Mutex
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					if r.Method == http.MethodPut {
						writes.Add(1)
						body, err := io.ReadAll(r.Body)
						if err != nil {
							t.Error(err)
						}
						native = string(body)
						if disconnect {
							conn, _, err := w.(http.Hijacker).Hijack()
							if err != nil {
								t.Error(err)
								return
							}
							_ = conn.Close()
						} else {
							w.WriteHeader(500)
							_, _ = io.WriteString(w, `<Error><Code>InternalError</Code></Error>`)
						}
						return
					}
					reads.Add(1)
					_, _ = io.WriteString(w, native)
				}))
				defer server.Close()
				newProvider := func() Provider {
					cfg := testBackend()
					cfg.Endpoint = server.URL
					p, err := NewS3(cfg, testCredentials)
					if err != nil {
						t.Fatal(err)
					}
					return p
				}
				p := newProvider()
				date := time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)
				var err error
				switch action {
				case "config":
					err = p.(BucketObjectLockProvider).PutBucketObjectLock(t.Context(), "bucket", api.ObjectBucketObjectLockConfiguration{Enabled: true})
				case "retention":
					err = p.(ObjectVersionLockProvider).PutObjectVersionRetention(t.Context(), "bucket", "key", "version", api.ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: &date}, false)
				case "hold":
					err = p.(ObjectVersionLockProvider).PutObjectVersionLegalHold(t.Context(), "bucket", "key", "version", api.ObjectVersionLegalHold{Status: "ON"})
				}
				if !errors.Is(err, ErrUnavailable) {
					t.Fatal("uncertain acknowledgment became success", err)
				}
				p = newProvider()
				switch action {
				case "config":
					c, e := p.(BucketObjectLockProvider).GetBucketObjectLock(t.Context(), "bucket")
					if e != nil || !c.Enabled {
						t.Fatal(c, e)
					}
				case "retention":
					r, e := p.(ObjectVersionLockProvider).GetObjectVersionRetention(t.Context(), "bucket", "key", "version")
					if e != nil || r.Mode != "COMPLIANCE" || r.RetainUntilDate == nil || !r.RetainUntilDate.Equal(date) {
						t.Fatal(r, e)
					}
				case "hold":
					h, e := p.(ObjectVersionLockProvider).GetObjectVersionLegalHold(t.Context(), "bucket", "key", "version")
					if e != nil || h.Status != "ON" {
						t.Fatal(h, e)
					}
				}
				if writes.Load() != 1 || reads.Load() != 1 {
					t.Fatal("read recovery repeated mutation", writes.Load(), reads.Load())
				}
			})
		}
	}
}

// adr: 563
func TestS3ObjectLockInvalidPoliciesDoNotDispatch(t *testing.T) {
	var requests atomic.Int32
	p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	zero, day := int32(0), int32(1)
	for _, c := range []api.ObjectBucketObjectLockConfiguration{
		{},
		{Enabled: true, DefaultRetention: &api.ObjectLockDefaultRetention{Mode: "GOVERNANCE", Days: &zero}},
		{Enabled: true, DefaultRetention: &api.ObjectLockDefaultRetention{Mode: "GOVERNANCE", Days: &day, Years: &day}},
		{Enabled: true, DefaultRetention: &api.ObjectLockDefaultRetention{Mode: "UNKNOWN", Days: &day}},
	} {
		if err := p.(BucketObjectLockProvider).PutBucketObjectLock(t.Context(), "bucket", c); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	overflow := time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC)
	for _, r := range []api.ObjectVersionRetention{
		{Mode: "GOVERNANCE"},
		{Mode: "COMPLIANCE", RetainUntilDate: &overflow},
		{Mode: "COMPLIANCE", EventHold: "ON", EventHoldDuration: &api.ObjectRetentionPeriod{Days: &zero}},
		{Mode: "COMPLIANCE", EventHold: "OFF", EventHoldDuration: &api.ObjectRetentionPeriod{Days: &day}},
	} {
		if err := p.(ObjectVersionLockProvider).PutObjectVersionRetention(t.Context(), "bucket", "key", "version", r, true); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if err := p.(ObjectVersionLockProvider).PutObjectVersionLegalHold(t.Context(), "bucket", "key", "version", api.ObjectVersionLegalHold{Status: "unknown"}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	for _, bucket := range []string{"", "bad\r\nbucket"} {
		if _, err := p.(BucketObjectLockProvider).GetBucketObjectLock(t.Context(), bucket); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
		if err := p.(BucketObjectLockProvider).PutBucketObjectLock(t.Context(), bucket, api.ObjectBucketObjectLockConfiguration{Enabled: true}); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid policy dispatched", requests.Load())
	}
}
