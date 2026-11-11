package apidserver

import (
	"net/http"
	"strings"

	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
)

// Organization storage paths no organization route registers explicitly.
// ServeMux prefers every more specific registration, so the explicit
// organization storage routes keep their own handlers.
const (
	orgBucketMirrorPattern       = "/v1/orgs/{organization}/apps/{slug}/buckets/{bucket}/"
	orgUploadRoutesMirrorPattern = "/v1/orgs/{organization}/apps/{slug}/upload-routes"
	orgUploadRouteMirrorPattern  = "/v1/orgs/{organization}/apps/{slug}/upload-routes/"
)

// orgObjectMirror serves a company app's storage operations that exist only
// as /v1/apps/{slug}/buckets/... and /v1/apps/{slug}/upload-routes routes
// (object storage audit OS-4): versions, tags and protection, versioning,
// lifecycle, CORS, encryption, object lock, notifications, compute triggers,
// multipart uploads, signed URLs, write receipts, capacity reconciliations,
// copy sources and upload routes. Only 14 storage routes were registered
// under the organization, so the rest answered 404 there.
//
// The mirror dispatches the request to the matching /v1/apps/{slug}/... route
// with the organization recorded on the context. appaccess then requires the
// app to belong to that organization, and authz.ObjectStorageAction
// authorizes the member's role for the route, as for the explicit
// organization routes. Paths outside storage are not mirrored.
func orgObjectMirror(next func() http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		organization, slug := r.PathValue("organization"), r.PathValue("slug")
		prefix := "/v1/orgs/" + organization + "/apps/" + slug
		rest, ok := strings.CutPrefix(r.URL.Path, prefix)
		if !ok || organization == "" || slug == "" || !orgObjectMirrored(rest) {
			http.NotFound(w, r)
			return
		}
		dispatched := r.Clone(authmw.WithOrganizationProjectPath(r.Context(), organization))
		dispatched.URL.Path = "/v1/apps/" + slug + rest
		dispatched.URL.RawPath = ""
		dispatched.RequestURI = dispatched.URL.RequestURI()
		next().ServeHTTP(w, dispatched)
	})
}

// orgObjectMirrored reports whether rest, the path after
// /v1/orgs/{organization}/apps/{slug}, is a storage path the mirror serves.
func orgObjectMirrored(rest string) bool {
	return strings.HasPrefix(rest, "/buckets/") || rest == "/upload-routes" || strings.HasPrefix(rest, "/upload-routes/")
}
