package objectstorage

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 407
func TestS3VersionTaggingSelectors(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		for _, version := range []string{"", "null", "native/+%?"} {
			t.Run(method+"/"+version, func(t *testing.T) {
				var calls atomic.Int32
				p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method != method || !r.URL.Query().Has("tagging") || r.URL.Query().Get("versionId") != version || r.Header.Get("Authorization") == "" {
						t.Error("signed exact tag selector lost", r.URL)
					}
					if version != "null" {
						w.Header().Set("X-Amz-Version-Id", version)
					}
					if method == http.MethodGet {
						_, _ = io.WriteString(w, `<Tagging><TagSet><Tag><Key>目录</Key><Value>old &amp; value</Value></Tag></TagSet></Tagging>`)
					} else if method == http.MethodPut {
						body, err := io.ReadAll(r.Body)
						tags, parse := ParseObjectTaggingXML(body)
						if err != nil || parse != nil || tags["目录"] != "replacement" || r.Header.Get("X-Amz-Metadata-Directive") != "" || r.Header.Get("X-Amz-Meta-"+ReservedUploadReceiptMetadataKey) != "" {
							t.Error("tag request changed completion metadata", tags, err, parse)
						}
					} else {
						w.WriteHeader(http.StatusNoContent)
					}
				})).(ObjectVersionTagger)
				out, err := dispatchVersionTags(t.Context(), p, "bucket", method, "key /+%.txt", version, map[string]string{"目录": "replacement"})
				if err != nil || calls.Load() != 1 || out.ProviderVersionID != version || method == http.MethodGet && out.Tags["目录"] != "old & value" {
					t.Fatal(out, err, calls.Load())
				}
			})
		}
	}
}

func TestS3VersionTaggingRejectsInvalidResponses(t *testing.T) {
	for _, tc := range []struct {
		name, method, version, body, code string
		status                            int
		duplicate                         bool
		want                              error
	}{
		{"wrong version", "GET", "wrong", `<Tagging><TagSet/></Tagging>`, "", 200, false, ErrUnavailable},
		{"missing version", "GET", "", `<Tagging><TagSet/></Tagging>`, "", 200, false, ErrUnavailable},
		{"duplicate header", "PUT", "native", "", "", 200, true, ErrUnavailable},
		{"wrong status", "DELETE", "native", "", "", 200, false, ErrUnavailable},
		{"wrong root", "GET", "native", `<Error><Code>AccessDenied</Code></Error>`, "", 200, false, ErrUnavailable},
		{"no tag set", "GET", "native", `<Tagging/>`, "", 200, false, ErrUnavailable},
		{"duplicate key", "GET", "native", `<Tagging><TagSet><Tag><Key>a</Key><Value>b</Value></Tag><Tag><Key>a</Key><Value>c</Value></Tag></TagSet></Tagging>`, "", 200, false, ErrUnavailable},
		{"oversized body", "GET", "native", strings.Repeat(" ", int(api.MaxObjectTaggingBodyBytes)+1), "", 200, false, ErrUnavailable},
		{"missing version upstream", "GET", "", "", "NoSuchVersion", 404, false, ErrNotFound},
		{"marker", "PUT", "", "", "MethodNotAllowed", 405, false, ErrObjectNotTaggable},
		{"invalid provider tags", "PUT", "", "", "InvalidTag", 400, false, ErrInvalid},
		{"conflicting provider action", "DELETE", "", "", "OperationAborted", 409, false, ErrConflict},
		{"permission", "DELETE", "", "", "AccessDenied", 403, false, ErrConfiguration},
		{"no implicit retry", "PUT", "", "", "InternalError", 500, false, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("X-Amz-Version-Id", tc.version)
				if tc.duplicate {
					w.Header().Add("X-Amz-Version-Id", tc.version)
				}
				w.WriteHeader(tc.status)
				if tc.code != "" {
					_, _ = io.WriteString(w, `<Error><Code>`+tc.code+`</Code><Message>private details</Message></Error>`)
				} else {
					_, _ = io.WriteString(w, tc.body)
				}
			})).(ObjectVersionTagger)
			_, err := dispatchVersionTags(t.Context(), p, "bucket", tc.method, "key", "native", map[string]string{})
			if !errors.Is(err, tc.want) || calls.Load() != 1 || strings.Contains(err.Error(), "private details") {
				t.Fatal(err, calls.Load())
			}
		})
	}
}

func TestObjectTaggingXMLAndLimits(t *testing.T) {
	valid := `<Tagging xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><TagSet><Tag><Key>name</Key><Value/></Tag></TagSet></Tagging>`
	if tags, err := ParseObjectTaggingXML([]byte(valid)); err != nil || len(tags) != 1 || tags["name"] != "" {
		t.Fatal(tags, err)
	}
	for _, body := range []string{
		`<Tagging/>`, `<Tagging><TagSet/><TagSet/></Tagging>`, `<Tagging><Unknown/><TagSet/></Tagging>`,
		`<Tagging><TagSet><Tag><Key>a</Key></Tag></TagSet></Tagging>`,
		`<Tagging><TagSet><Tag><Key>a</Key><Key>b</Key><Value>c</Value></Tag></TagSet></Tagging>`,
		`<Tagging><TagSet><Tag><Key><Nested/></Key><Value>b</Value></Tag></TagSet></Tagging>`,
		`<Tagging xmlns="urn:foreign"><TagSet/></Tagging>`, `<Tagging><TagSet/></Tagging><Tagging><TagSet/></Tagging>`,
		`<Tagging><TagSet>` + strings.Repeat(`<Tag><Key>a</Key><Value>b</Value></Tag>`, api.MaxObjectTags+1) + `</TagSet></Tagging>`,
	} {
		if _, err := ParseObjectTaggingXML([]byte(body)); !errors.Is(err, ErrInvalid) {
			t.Fatal(body, err)
		}
	}
	for _, value := range []string{"nul\x00", "control\x01", "invalid\xff", "\ufffe", strings.Repeat("x", api.MaxObjectTagValueBytes+1)} {
		if err := ValidateObjectMetadata(ObjectMetadata{Tags: map[string]string{"key": value}}); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid tag value accepted", err)
		}
	}
}
