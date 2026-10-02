package objectstorage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

type legacyTagProvider struct {
	Provider
	calls int
}

func (p *legacyTagProvider) GetObjectTags(context.Context, string, string) (map[string]string, error) {
	p.calls++
	return map[string]string{"team": "old"}, nil
}
func (p *legacyTagProvider) PutObjectTags(context.Context, string, string, map[string]string) error {
	p.calls++
	return nil
}
func (p *legacyTagProvider) DeleteObjectTags(context.Context, string, string) error {
	p.calls++
	return nil
}

type taggingReferenceStub struct {
	state.ObjectVersionReferenceStore
	native string
}

func (s taggingReferenceStub) ResolveObjectVersion(context.Context, string, string, string, string) (string, error) {
	return s.native, nil
}

// adr: 407
func TestObjectTaggingCapabilityAndAdmission(t *testing.T) {
	f := newUploadFixture(t)
	b, err := f.store.GetObjectBucket(t.Context(), f.account.ID, f.app.ID, f.route.BucketID)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		p := &legacyTagProvider{}
		s := TaggingService{References: f.store, Provider: p}
		out, err := s.Do(t.Context(), b, method, "key", "", map[string]string{"team": "new"})
		if err != nil || p.calls != 1 || out.VersionID != "" || out.Tags == nil || method == http.MethodPut && out.Tags["team"] != "new" || method == http.MethodDelete && len(out.Tags) != 0 {
			t.Fatal("legacy current tagging", method, out, err)
		}
		if _, err = s.Do(t.Context(), b, method, "key", "null", nil); !errors.Is(err, ErrUnsupported) || p.calls != 1 {
			t.Fatal("selected legacy tagging dispatched", method, err)
		}
		s.BeforeRequest = func(context.Context) error { return state.ErrObjectBudget }
		if _, err = s.Do(t.Context(), b, method, "key", "", nil); !errors.Is(err, state.ErrObjectBudget) || p.calls != 1 {
			t.Fatal("budget failure dispatched", method, err)
		}
	}
	p := historyTestProvider(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("invalid reference dispatched") })).(Provider)
	for _, tc := range []struct{ selector, native string }{
		{uuid.NewString(), ""}, {uuid.NewString(), "null"}, {"null", "immutable"}, {uuid.NewString(), "bad\nheader"},
	} {
		s := TaggingService{References: taggingReferenceStub{ObjectVersionReferenceStore: f.store, native: tc.native}, Provider: p, BeforeRequest: func(context.Context) error { t.Error("invalid reference admitted"); return nil }}
		if _, err := s.Do(t.Context(), b, http.MethodGet, "key", tc.selector, nil); !errors.Is(err, ErrUnavailable) {
			t.Fatal(tc, err)
		}
	}
}

func TestTaggingRequestRejectsIgnoredSemantics(t *testing.T) {
	for _, header := range []string{"If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since", "X-Amz-Copy-Source", "X-Amz-Tagging", "X-Amz-Metadata-Directive", "X-Amz-Tagging-Directive"} {
		r := httptest.NewRequest(http.MethodPut, "/", nil)
		r.Header.Add(header, "")
		r.Header.Add(header, "predicate")
		if err := ValidateObjectTaggingRequest(r); !errors.Is(err, ErrUnsupported) {
			t.Fatal(header, err)
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		r := httptest.NewRequest(method, "/", strings.NewReader("body"))
		if err := ValidateObjectTaggingRequest(r); !errors.Is(err, ErrInvalid) {
			t.Fatal(method, err)
		}
	}
}
