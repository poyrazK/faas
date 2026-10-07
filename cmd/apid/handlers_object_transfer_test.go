package main

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// adr: 553
func TestObjectBucketCatalogTransferProfile(t *testing.T) {
	e := setup(t, api.PlanHobby)
	setS3Flag(t, e, true)
	createApp(t, e, "transfer-discovery")
	r := objectRegistry(t, &fakeObjectProvider{}, &fakeObjectProvider{}, "external")
	r.MaxUploadBytes, r.MaxSinglePutBytes, r.MaxPartBytes = 5<<40, 512<<20, 512<<20
	r.Transfer = objectstorage.ObjectTransferConfig{Profile: "direct", TimeoutSeconds: 7200, MaxConcurrentUploads: 4, MaxSpoolBytes: 2 << 30, MinSpoolFreeBytes: 1 << 30}
	e.s.WithObjectStorage(r)
	response := e.do(t, "GET", "/v1/apps/transfer-discovery/buckets", nil, nil)
	var got api.ObjectBucketList
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &got) != nil || !got.Enabled || got.MaxUploadBytes != 5<<40 || got.MaxSinglePutBytes != 512<<20 || got.MaxPartBytes != 512<<20 || got.TransferTimeoutSeconds != 7200 || got.UploadProfile != "direct" {
		t.Fatalf("catalog = %d %s", response.Code, response.Body.String())
	}
}
