package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 628
func TestMultipartPartURLUsesPartLimitRatherThanSinglePutLimit(t *testing.T) {
	_, teardown := withTestIdentities(t)
	defer teardown()
	e := setup(t, api.PlanHobby)
	if err := e.s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	createApp(t, e, "part-limit")
	registry := objectRegistry(t, &fakeObjectProvider{}, &fakeObjectProvider{}, "external")
	registry.MaxUploadBytes = 16 << 20
	registry.MaxSinglePutBytes, registry.MaxPartBytes = 1<<20, api.MinMultipartPartBytes
	e.s.WithObjectStorage(registry)
	b := bucketResponse(t, e.do(t, "POST", "/v1/apps/part-limit/buckets", map[string]any{"name": "assets"}, nil), http.StatusCreated)
	qualifyObjectAccounting(t, e, b.ID)
	registry.Accounting.MaxAccountBytes, registry.Accounting.MaxBucketBytes = 64<<20, 32<<20
	registry.Accounting.MaxMonthlyEgressBytes = 32 << 20
	base := "/v1/apps/part-limit/buckets/" + b.ID
	response := e.do(t, "POST", base+"/multipart-uploads", api.CreateObjectMultipartUploadRequest{Key: "large", SizeBytes: api.MinMultipartPartBytes + 3}, nil)
	var upload api.ObjectMultipartUpload
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &upload) != nil || upload.PartCount != 2 {
		t.Fatal("multipart admission failed", response.Code, response.Body.String())
	}
	for _, tc := range []struct {
		name, path string
		body       any
		status     int
	}{
		{"full part", base + "/multipart-uploads/" + upload.ID + "/parts/1/signed-url", api.ObjectMultipartPartSignRequest{}, http.StatusOK},
		{"short final part", base + "/multipart-uploads/" + upload.ID + "/parts/2/signed-url", api.ObjectMultipartPartSignRequest{}, http.StatusOK},
		{"oversized ordinary PUT", base + "/signed-url", api.ObjectSignRequest{Method: "PUT", Key: "other", SizeBytes: &upload.PartSizeBytes}, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := e.do(t, "POST", tc.path, tc.body, nil)
			if out.Code != tc.status {
				t.Fatal(out.Code, out.Body.String())
			}
		})
	}
}
