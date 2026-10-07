package objectstorage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type versionPageProvider struct {
	Provider
	page  ObjectVersionListPage
	calls int
	input ObjectVersionListRequest
}

func (p *versionPageProvider) ListObjectVersionPage(_ context.Context, _ string, r ObjectVersionListRequest) (ObjectVersionListPage, error) {
	p.calls++
	p.input = r
	return p.page, nil
}

// adr: 638
func TestPublicVersionListingOwnsPaginationAndAdmission(t *testing.T) {
	f := newUploadFixture(t)
	b, err := f.store.GetObjectBucket(t.Context(), f.account.ID, f.app.ID, f.route.BucketID)
	if err != nil {
		t.Fatal(err)
	}
	p := &versionPageProvider{page: ObjectVersionListPage{Items: []ListedObjectVersion{
		{Object: Object{Key: "目录 +%.txt", Size: 3, ETag: `"old"`, LastModified: time.Now()}, ProviderVersionID: "private-old/+%", IsLatest: false},
		{Object: Object{Key: "目录 +%.txt", Size: 3, ETag: `"new"`, LastModified: time.Now()}, ProviderVersionID: "private-new/+%", IsLatest: true},
	}, NextKeyMarker: "目录 +%.txt", NextProviderVersionMarker: "private-cursor/+%"}}
	admissions := 0
	s := VersionListingService{References: f.store, Provider: p, BeforeRequest: func(context.Context) error { admissions++; return nil }}
	r := api.ObjectVersionListRequest{Prefix: "目录", Limit: 2}
	page, err := s.Do(t.Context(), b, r)
	if err != nil || len(page.Items) != 2 || page.Items[0].IsLatest || !page.Items[1].IsLatest || admissions != 1 {
		t.Fatal(page, err, admissions)
	}
	for _, id := range []string{page.Items[0].VersionID, page.Items[1].VersionID, page.NextVersionIDMarker} {
		if !state.ValidObjectVersionID(id) || id == "null" {
			t.Fatal("native version escaped", id)
		}
	}
	if page.Items[0].VersionID == page.Items[1].VersionID {
		t.Fatal("versions collapsed")
	}
	r.KeyMarker, r.VersionIDMarker = page.NextKeyMarker, page.NextVersionIDMarker
	p.page = ObjectVersionListPage{}
	if next, e := s.Do(t.Context(), b, r); e != nil || p.input.ProviderVersionMarker != "private-cursor/+%" || next.Items == nil || next.CommonPrefixes == nil {
		t.Fatal(next, e, p.input)
	}
	for _, selector := range []api.ObjectVersionListRequest{
		{Limit: 1, KeyMarker: "other-key", VersionIDMarker: page.Items[0].VersionID},
		{Limit: 1, KeyMarker: page.Items[0].Key, VersionIDMarker: uuid.NewString()},
		{Limit: 1, VersionIDMarker: page.Items[0].VersionID},
		{Limit: 0}, {Limit: 1001}, {Limit: 1, Delimiter: "//"},
	} {
		before := p.calls
		if _, e := s.Do(t.Context(), b, selector); e == nil || p.calls != before {
			t.Fatal("invalid marker dispatched", selector, e)
		}
	}
	foreign := b
	foreign.AccountID = uuid.NewString()
	before := p.calls
	if _, e := s.Do(t.Context(), foreign, r); e == nil || p.calls != before {
		t.Fatal("foreign reference dispatched", e)
	}
	s.BeforeRequest = func(context.Context) error { return state.ErrObjectBudget }
	if _, e := s.Do(t.Context(), b, api.ObjectVersionListRequest{Limit: 1}); !errors.Is(e, state.ErrObjectBudget) || p.calls != before {
		t.Fatal("budget bypass", e)
	}
}

// adr: 638
func TestPublicVersionListingRejectsMalformedProviderPages(t *testing.T) {
	f := newUploadFixture(t)
	b, _ := f.store.GetObjectBucket(t.Context(), f.account.ID, f.app.ID, f.route.BucketID)
	valid := ListedObjectVersion{Object: Object{Key: "key", Size: 1, ETag: `"etag"`, LastModified: time.Now()}, ProviderVersionID: "native"}
	for _, page := range []ObjectVersionListPage{
		{Items: []ListedObjectVersion{valid, valid}},
		{Items: []ListedObjectVersion{valid}, NextKeyMarker: "key", NextProviderVersionMarker: "native"},
		{NextKeyMarker: "key"}, {NextProviderVersionMarker: "native"},
		{CommonPrefixes: []string{"key/"}},
		{Items: []ListedObjectVersion{{Object: Object{Key: "key", Size: 1}, ProviderVersionID: "native"}}},
	} {
		if out, e := PublicObjectVersionPage(t.Context(), f.store, b, ObjectVersionListRequest{Limit: 1, KeyMarker: "key", ProviderVersionMarker: "native"}, page); !errors.Is(e, ErrUnavailable) || len(out.Items) != 0 {
			t.Fatal(page, out, e)
		}
	}
	marker := valid
	marker.ProviderVersionID, marker.DeleteMarker, marker.ETag = "marker", true, ""
	page := ObjectVersionListPage{Items: []ListedObjectVersion{marker}, CommonPrefixes: []string{"key/"}}
	if out, e := PublicObjectVersionPage(t.Context(), f.store, b, ObjectVersionListRequest{Limit: 2, Delimiter: "/"}, page); e != nil || !out.Items[0].DeleteMarker || len(out.CommonPrefixes) != 1 {
		t.Fatal(out, e)
	}
}
