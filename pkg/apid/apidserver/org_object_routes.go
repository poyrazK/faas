package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

func (s *server) registerOrgObjectRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/orgs/{organization}/apps/{slug}/buckets", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageListSurface...)(s.object.ListBuckets))))
	mux.HandleFunc("POST /v1/orgs/{organization}/apps/{slug}/buckets", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageManageSurface...)(s.object.CreateBucket))))
	mux.HandleFunc("DELETE /v1/orgs/{organization}/apps/{slug}/buckets/{bucket}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageManageSurface...)(s.object.DeleteBucket))))
	mux.HandleFunc("GET /v1/orgs/{organization}/apps/{slug}/buckets/{bucket}/access-grants", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageManageSurface...)(s.object.ListBucketAccessGrants))))
	mux.HandleFunc("PUT /v1/orgs/{organization}/apps/{slug}/buckets/{bucket}/access-grants/{key}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageManageSurface...)(s.object.SetBucketAccessGrant))))
	mux.HandleFunc("DELETE /v1/orgs/{organization}/apps/{slug}/buckets/{bucket}/access-grants/{key}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageManageSurface...)(s.object.DeleteBucketAccessGrant))))
	mux.HandleFunc("GET /v1/orgs/{organization}/apps/{slug}/buckets/{bucket}/s3-credentials", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageManageSurface...)(s.object.ListObjectS3Credentials))))
	mux.HandleFunc("POST /v1/orgs/{organization}/apps/{slug}/buckets/{bucket}/s3-credentials", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageManageSurface...)(s.object.CreateObjectS3Credential))))
	mux.HandleFunc("DELETE /v1/orgs/{organization}/apps/{slug}/buckets/{bucket}/s3-credentials/{credential}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageManageSurface...)(s.object.RevokeObjectS3Credential))))
	mux.HandleFunc("GET /v1/orgs/{organization}/apps/{slug}/buckets/{bucket}/compute-bindings", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageManageSurface...)(s.object.ListObjectStorageComputeBindings))))
	mux.HandleFunc("POST /v1/orgs/{organization}/apps/{slug}/buckets/{bucket}/compute-bindings", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageManageSurface...)(s.object.CreateObjectStorageComputeBinding))))
	mux.HandleFunc("DELETE /v1/orgs/{organization}/apps/{slug}/buckets/{bucket}/compute-bindings/{binding}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageManageSurface...)(s.object.DeleteObjectStorageComputeBinding))))
	mux.HandleFunc("POST /v1/orgs/{organization}/apps/{slug}/buckets/{bucket}/compute-bindings/{binding}/rotate", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageManageSurface...)(s.object.RotateObjectStorageComputeBinding))))
	mux.HandleFunc("GET /v1/orgs/{organization}/apps/{slug}/buckets/{bucket}/objects", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageReadSurface...)(s.object.ListBucketObjects))))
	mux.HandleFunc("GET /v1/orgs/{organization}/object-storage-usage", s.authLimited(s.requireMFA(s.requireScope(api.ScopesUsageReadSurface...)(s.object.GetObjectStorageUsage))))
}
