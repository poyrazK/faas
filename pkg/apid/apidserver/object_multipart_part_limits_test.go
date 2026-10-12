package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// adr: 628
func TestMultipartPartURLUsesPartLimitRatherThanSinglePutLimit(t *testing.T) {
	_, teardown := apidserver.WithTestIdentities(t)
	defer teardown()
	e := apidserver.Setup(t, api.PlanHobby)
	if err := (*(*e.S()).RuntimeConfig()).Apply(apidserver.RuntimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	apidserver.CreateApp(t, e, "part-limit")
	registry := apidserver.ObjectRegistry(t, &apidserver.FakeObjectProvider{}, &apidserver.FakeObjectProvider{}, "external")
	registry.MaxUploadBytes = 16 << 20
	registry.MaxSinglePutBytes, registry.MaxPartBytes = 1<<20, api.MinMultipartPartBytes
	(*e.S()).WithObjectStorage(registry)
	b := apidserver.BucketResponse(t, e.Do(t, "POST", "/v1/apps/part-limit/buckets", map[string]any{"name": "assets"}, nil), http.StatusCreated)
	apidserver.QualifyObjectAccounting(t, e, b.ID)
	registry.Accounting.MaxAccountBytes, registry.Accounting.MaxBucketBytes = 64<<20, 32<<20
	registry.Accounting.MaxMonthlyEgressBytes = 32 << 20
	base := "/v1/apps/part-limit/buckets/" + b.ID
	response := e.Do(t, "POST", base+"/multipart-uploads", api.CreateObjectMultipartUploadRequest{Key: "large", SizeBytes: api.MinMultipartPartBytes + 3}, nil)
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
			out := e.Do(t, "POST", tc.path, tc.body, nil)
			if out.Code != tc.status {
				t.Fatal(out.Code, out.Body.String())
			}
		})
	}
}
