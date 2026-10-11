package objectstorage

import (
	"context"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type VersionListingService struct {
	References    state.ObjectVersionReferenceStore
	Provider      Provider
	BeforeRequest func(context.Context) error
}

func (s VersionListingService) Do(ctx context.Context, b state.ObjectBucket, r api.ObjectVersionListRequest) (api.ObjectVersionList, error) {
	input := ObjectVersionListRequest{Prefix: r.Prefix, Delimiter: r.Delimiter, KeyMarker: r.KeyMarker, Limit: r.Limit}
	if input.Limit < 1 || input.Limit > api.MaxObjectS3ListItems || !validVersionListText(input.Prefix) || !validVersionListText(input.KeyMarker) || !validVersionDelimiter(input.Delimiter) || r.VersionIDMarker != "" && (r.KeyMarker == "" || !state.ValidObjectVersionID(r.VersionIDMarker)) {
		return api.ObjectVersionList{}, ErrInvalid
	}
	lister, ok := s.Provider.(ObjectVersionLister)
	if !ok || s.References == nil {
		return api.ObjectVersionList{}, ErrUnsupported
	}
	if r.VersionIDMarker != "" {
		var err error
		input.ProviderVersionMarker, err = s.References.ResolveObjectVersion(ctx, b.AccountID, b.ID, r.KeyMarker, r.VersionIDMarker)
		if err != nil {
			return api.ObjectVersionList{}, err
		}
		if !validNativeVersionID(input.ProviderVersionMarker) || (r.VersionIDMarker == "null") != (input.ProviderVersionMarker == "null") {
			return api.ObjectVersionList{}, ErrUnavailable
		}
	}
	if s.BeforeRequest == nil {
		return api.ObjectVersionList{}, ErrUnavailable
	}
	if err := s.BeforeRequest(ctx); err != nil {
		return api.ObjectVersionList{}, err
	}
	page, err := lister.ListObjectVersionPage(ctx, b.PhysicalName, input)
	if err != nil {
		return api.ObjectVersionList{}, err
	}
	return PublicObjectVersionPage(ctx, s.References, b, input, page)
}

// PublicObjectVersionPage shares identity translation between control JSON and S3 XML.
// A failed reference commit never exposes native IDs or an incomplete public page.
func PublicObjectVersionPage(ctx context.Context, st state.ObjectVersionReferenceStore, b state.ObjectBucket, input ObjectVersionListRequest, page ObjectVersionListPage) (api.ObjectVersionList, error) {
	out := api.ObjectVersionList{Items: []api.ObjectVersion{}, CommonPrefixes: []string{}}
	if st == nil || len(page.Items)+len(page.CommonPrefixes) > int(input.Limit) || page.NextProviderVersionMarker != "" && page.NextKeyMarker == "" || !validVersionListText(page.NextKeyMarker) || page.NextKeyMarker != "" && (!strings.HasPrefix(page.NextKeyMarker, input.Prefix) || len(page.Items)+len(page.CommonPrefixes) == 0 || page.NextKeyMarker == input.KeyMarker && page.NextProviderVersionMarker == input.ProviderVersionMarker) {
		return out, ErrUnavailable
	}
	items := make([]state.ObjectVersionIdentity, 0, len(page.Items)+1)
	seen := map[string]bool{}
	for _, v := range page.Items {
		if !ValidKey(v.Key) || !strings.HasPrefix(v.Key, input.Prefix) || !validNativeVersionID(v.ProviderVersionID) || v.LastModified.IsZero() || v.Size < 0 || v.Size > api.MaxObjectUploadBytes || !v.DeleteMarker && !validUploadETag(v.ETag) || !validVersionListText(v.StorageClass) || seen[v.Key+"\x00"+v.ProviderVersionID] {
			return out, ErrUnavailable
		}
		items = append(items, state.ObjectVersionIdentity{Key: v.Key, ProviderVersionID: v.ProviderVersionID, DeleteMarker: v.DeleteMarker})
		seen[v.Key+"\x00"+v.ProviderVersionID] = true
	}
	for _, prefix := range page.CommonPrefixes {
		if input.Delimiter == "" || !validVersionListText(prefix) || !strings.HasPrefix(prefix, input.Prefix) || !strings.HasSuffix(prefix, input.Delimiter) || seen["prefix\x00"+prefix] {
			return out, ErrUnavailable
		}
		seen["prefix\x00"+prefix] = true
	}
	if page.NextProviderVersionMarker != "" && !seen[page.NextKeyMarker+"\x00"+page.NextProviderVersionMarker] {
		if !validNativeVersionID(page.NextProviderVersionMarker) {
			return out, ErrUnavailable
		}
		items = append(items, state.ObjectVersionIdentity{Key: page.NextKeyMarker, ProviderVersionID: page.NextProviderVersionMarker})
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectUploadSettlementTimeout)
	defer cancel()
	refs, err := st.RecordObjectVersions(finish, b.AccountID, b.ID, items)
	if err != nil || len(refs) != len(items) {
		return out, ErrUnavailable
	}
	ids := map[string]string{}
	for i, v := range refs {
		if v.Key != items[i].Key || v.ProviderVersionID != items[i].ProviderVersionID || !state.ValidObjectVersionID(v.ID) || (v.ID == "null") != (v.ProviderVersionID == "null") {
			return out, ErrUnavailable
		}
		ids[v.Key+"\x00"+v.ProviderVersionID] = v.ID
	}
	if len(ids) != len(items) {
		return out, ErrUnavailable
	}
	for _, v := range page.Items {
		out.Items = append(out.Items, api.ObjectVersion{Key: v.Key, VersionID: ids[v.Key+"\x00"+v.ProviderVersionID], IsLatest: v.IsLatest, DeleteMarker: v.DeleteMarker, SizeBytes: v.Size, ETag: v.ETag, LastModified: v.LastModified, StorageClass: v.StorageClass})
	}
	out.CommonPrefixes = append(out.CommonPrefixes, page.CommonPrefixes...)
	out.NextKeyMarker = page.NextKeyMarker
	out.NextVersionIDMarker = ids[page.NextKeyMarker+"\x00"+page.NextProviderVersionMarker]
	return out, nil
}
