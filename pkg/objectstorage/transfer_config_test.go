package objectstorage

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 553
func TestObjectTransferConfigurationBoundaries(t *testing.T) {
	base := Config{DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "external-a"}, MaxUploadBytes: 5 << 40, Backends: []BackendConfig{testBackend()}}
	for _, tc := range []struct {
		name         string
		transfer     ObjectTransferConfig
		single, part int64
		invalid      bool
	}{
		{"proxied defaults", ObjectTransferConfig{Profile: "proxied"}, 0, 0, false},
		{"direct large", ObjectTransferConfig{Profile: "direct", TimeoutSeconds: 3600, MaxConcurrentUploads: 2, MaxSpoolBytes: api.MaxObjectUploadSpoolBytes}, 5 << 30, 5 << 30, false},
		{"proxied oversized", ObjectTransferConfig{Profile: "proxied"}, 65 << 20, 64 << 20, true},
		{"unknown profile", ObjectTransferConfig{Profile: "unknown"}, 0, 0, true},
		{"negative timeout", ObjectTransferConfig{TimeoutSeconds: -1}, 0, 0, true},
		{"timeout overflow", ObjectTransferConfig{TimeoutSeconds: 1 << 60}, 0, 0, true},
		{"too many uploads", ObjectTransferConfig{MaxConcurrentUploads: api.MaxObjectConcurrentUploads + 1}, 0, 0, true},
		{"small spool", ObjectTransferConfig{MaxSpoolBytes: 1}, 0, 0, true},
		{"large spool", ObjectTransferConfig{MaxSpoolBytes: api.MaxObjectUploadSpoolBytes + 1}, 0, 0, true},
		{"negative free reserve", ObjectTransferConfig{MinSpoolFreeBytes: -1}, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := base
			config.Transfer, config.MaxSinglePutBytes, config.MaxPartBytes = tc.transfer, tc.single, tc.part
			r, err := NewRegistry(config, testCredentials, map[string]Factory{"s3": NewS3})
			if (err != nil) != tc.invalid {
				t.Fatalf("registry=%+v err=%v", r, err)
			}
			if err != nil {
				return
			}
			if r.Transfer.Profile == "proxied" && (r.MaxSinglePutBytes != 64<<20 || r.MaxPartBytes != 64<<20) {
				t.Fatal("proxied defaults exceed body contract", r)
			}
			if tc.transfer.TimeoutSeconds != 0 && r.TransferTimeout() != time.Duration(tc.transfer.TimeoutSeconds)*time.Second {
				t.Fatal("configured timeout lost", r.TransferTimeout())
			}
		})
	}
}

func TestObjectStreamUsesRemainingConfiguredDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Hour)
	defer cancel()
	if timeout := objectStreamTimeout(ctx); timeout <= api.ObjectTransferTimeout || timeout > 2*time.Hour {
		t.Fatal(timeout)
	}
	if objectStreamTimeout(t.Context()) != api.ObjectTransferTimeout {
		t.Fatal("legacy stream bound changed")
	}
}

func TestObjectTransferDirectDeploymentExample(t *testing.T) {
	data, err := os.ReadFile("../../deploy/object-storage.direct.example.json")
	if err != nil {
		t.Fatal(err)
	}
	var config Config
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	r, err := NewRegistry(config, func(string) string { return "local-fixture" }, map[string]Factory{"s3": NewS3})
	if err != nil || r.Transfer.Profile != "direct" || r.MaxUploadBytes > int64(api.MaxMultipartParts)*r.MaxPartBytes || r.Transfer.MaxSpoolBytes < r.MaxSinglePutBytes {
		t.Fatal("direct example cannot reach its configured total limit", r, err)
	}
}
