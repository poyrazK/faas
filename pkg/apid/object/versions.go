package object

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) ListBucketObjectVersions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.ObjectStorageEnabled() {
		BucketProblem(w, ErrObjectStorageDisabled)
		return
	}
	b, _, p, ok := s.loadBucket(w, r, acct, true)
	if !ok || !s.AuthorizeBucketData(w, r, b, state.ObjectBucketPermissionRead) {
		return
	}
	req, err := controlObjectVersionListRequest(r)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	refs, _ := s.Host.Store().(state.ObjectVersionReferenceStore)
	service := objectstorage.VersionListingService{Provider: p, References: refs, BeforeRequest: s.CustomerObjectRequestRecorder(b)}
	out, err := service.Do(r.Context(), b, req)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

func controlObjectVersionListRequest(r *http.Request) (api.ObjectVersionListRequest, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	out := api.ObjectVersionListRequest{Limit: api.MaxObjectS3ListItems}
	if err != nil || r.ContentLength != 0 {
		return out, objectstorage.ErrInvalid
	}
	for k, v := range q {
		if len(v) != 1 {
			return out, objectstorage.ErrInvalid
		}
		switch k {
		case "prefix":
			out.Prefix = v[0]
		case "delimiter":
			out.Delimiter = v[0]
		case "key_marker":
			out.KeyMarker = v[0]
		case "version_id_marker":
			out.VersionIDMarker = v[0]
		case "limit":
			n, e := strconv.ParseInt(v[0], 10, 32)
			if e != nil {
				return out, objectstorage.ErrInvalid
			}
			out.Limit = int32(n)
		default:
			return out, objectstorage.ErrInvalid
		}
	}
	return out, nil
}
