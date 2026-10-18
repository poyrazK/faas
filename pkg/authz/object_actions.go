package authz

import "strings"

// ObjectStorageAction is deliberately closed: unrelated object settings,
// transfers, upload routes and cloning require their own shared-access review.
func ObjectStorageAction(pattern string) (OrgAction, bool) {
	pattern = strings.Replace(pattern, "/v1/orgs/{organization}/apps/", "/v1/apps/", 1)
	action, ok := objectStorageActions[pattern]
	return action, ok
}

func ObjectStoragePattern(pattern string) bool {
	return strings.Contains(pattern, "/buckets") || strings.Contains(pattern, "/upload-routes")
}

var objectStorageActions = map[string]OrgAction{
	"GET /v1/apps/{slug}/buckets":                                             ReadObjectStorage,
	"POST /v1/apps/{slug}/buckets":                                            ManageObjectStorage,
	"DELETE /v1/apps/{slug}/buckets/{bucket}":                                 ManageObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/objects":                            ReadObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/s3-credentials":                     ManageObjectStorage,
	"POST /v1/apps/{slug}/buckets/{bucket}/s3-credentials":                    ManageObjectStorage,
	"DELETE /v1/apps/{slug}/buckets/{bucket}/s3-credentials/{credential}":     ManageObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/access-grants":                      ManageObjectStorage,
	"PUT /v1/apps/{slug}/buckets/{bucket}/access-grants/{key}":                ManageObjectStorage,
	"DELETE /v1/apps/{slug}/buckets/{bucket}/access-grants/{key}":             ManageObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/compute-bindings":                   ReadObjectStorage,
	"POST /v1/apps/{slug}/buckets/{bucket}/compute-bindings":                  BindObjectStorage,
	"DELETE /v1/apps/{slug}/buckets/{bucket}/compute-bindings/{binding}":      BindObjectStorage,
	"POST /v1/apps/{slug}/buckets/{bucket}/compute-bindings/{binding}/rotate": ManageObjectStorage,
}
