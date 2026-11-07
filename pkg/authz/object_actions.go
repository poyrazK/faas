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
	// Reviewed in bug hunt 37: these were in neither map, so a company app's
	// members, owners included, were refused them. Configuration reads take
	// ReadObjectStorage; writes, signed URLs, deletions, uploads and anything
	// that names a credential take ManageObjectStorage.
	"DELETE /v1/apps/{slug}/buckets/{bucket}/capacity-reconciliations/{reconciliation}":         ManageObjectStorage,
	"DELETE /v1/apps/{slug}/buckets/{bucket}/cors":                                              ManageObjectStorage,
	"DELETE /v1/apps/{slug}/buckets/{bucket}/encryption":                                        ManageObjectStorage,
	"DELETE /v1/apps/{slug}/buckets/{bucket}/lifecycle":                                         ManageObjectStorage,
	"DELETE /v1/apps/{slug}/buckets/{bucket}/multipart-uploads/{upload}":                        ManageObjectStorage,
	"DELETE /v1/apps/{slug}/buckets/{bucket}/notifications":                                     ManageObjectStorage,
	"DELETE /v1/apps/{slug}/buckets/{bucket}/objects":                                           ManageObjectStorage,
	"DELETE /v1/apps/{slug}/buckets/{bucket}/objects/tags":                                      ManageObjectStorage,
	"DELETE /v1/apps/{slug}/buckets/{bucket}/objects/versions":                                  ManageObjectStorage,
	"DELETE /v1/apps/{slug}/buckets/{bucket}/s3-credentials/{credential}/copy-sources/{source}": ManageObjectStorage,
	"DELETE /v1/apps/{slug}/upload-routes/{route}":                                              ManageObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/capacity-reconciliations/{reconciliation}":            ReadObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/cors":                                                 ReadObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/encryption":                                           ReadObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/encryption-capabilities":                              ReadObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/lifecycle":                                            ReadObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/lifecycle/scans/{scan}":                               ReadObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/multipart-uploads":                                    ReadObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/multipart-uploads/{upload}":                           ReadObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/multipart-uploads/{upload}/parts":                     ReadObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/notifications":                                        ReadObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/object-lock":                                          ReadObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/object-lock-capabilities":                             ReadObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/objects/deletions/{deletion}":                         ReadObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/objects/protection/legal-hold":                        ReadObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/objects/protection/retention":                         ReadObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/objects/tags":                                         ReadObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/objects/versions":                                     ReadObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/protection-operations/{operation}":                    ReadObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/s3-credentials/{credential}/copy-sources":             ManageObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/versioning":                                           ReadObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/write-receipts":                                       ReadObjectStorage,
	"GET /v1/apps/{slug}/buckets/{bucket}/write-receipts/{receipt}":                             ReadObjectStorage,
	"GET /v1/apps/{slug}/upload-routes":                                                         ReadObjectStorage,
	"POST /v1/apps/{slug}/buckets/{bucket}/capacity-reconciliations":                            ManageObjectStorage,
	"POST /v1/apps/{slug}/buckets/{bucket}/lifecycle/scans":                                     ManageObjectStorage,
	"POST /v1/apps/{slug}/buckets/{bucket}/multipart-uploads":                                   ManageObjectStorage,
	"POST /v1/apps/{slug}/buckets/{bucket}/multipart-uploads/{upload}/complete":                 ManageObjectStorage,
	"POST /v1/apps/{slug}/buckets/{bucket}/multipart-uploads/{upload}/parts/{part}/signed-url":  ManageObjectStorage,
	"POST /v1/apps/{slug}/buckets/{bucket}/objects/deletions":                                   ManageObjectStorage,
	"POST /v1/apps/{slug}/buckets/{bucket}/signed-url":                                          ManageObjectStorage,
	"POST /v1/apps/{slug}/upload-routes":                                                        ManageObjectStorage,
	"PUT /v1/apps/{slug}/buckets/{bucket}/compute-triggers":                                     ManageObjectStorage,
	"PUT /v1/apps/{slug}/buckets/{bucket}/cors":                                                 ManageObjectStorage,
	"PUT /v1/apps/{slug}/buckets/{bucket}/encryption":                                           ManageObjectStorage,
	"PUT /v1/apps/{slug}/buckets/{bucket}/lifecycle":                                            ManageObjectStorage,
	"PUT /v1/apps/{slug}/buckets/{bucket}/notifications":                                        ManageObjectStorage,
	"PUT /v1/apps/{slug}/buckets/{bucket}/object-lock":                                          ManageObjectStorage,
	"PUT /v1/apps/{slug}/buckets/{bucket}/objects/protection/legal-hold":                        ManageObjectStorage,
	"PUT /v1/apps/{slug}/buckets/{bucket}/objects/protection/retention":                         ManageObjectStorage,
	"PUT /v1/apps/{slug}/buckets/{bucket}/objects/tags":                                         ManageObjectStorage,
	"PUT /v1/apps/{slug}/buckets/{bucket}/s3-credentials/{credential}/copy-sources/{source}":    ManageObjectStorage,
	"PUT /v1/apps/{slug}/buckets/{bucket}/versioning":                                           ManageObjectStorage,
	// The dashboard's bucket create and delete, as their /v1 twins.
	"POST /dashboard/apps/{slug}/storage/buckets":                 ManageObjectStorage,
	"POST /dashboard/apps/{slug}/storage/buckets/{bucket}/delete": ManageObjectStorage,
}
