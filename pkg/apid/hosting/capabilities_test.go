package hosting

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCapabilitiesUseEnforcedLimits(t *testing.T) {
	h := &testHost{enabled: true, allowed: true, store: state.NewMemStore(), artifacts: &testArtifacts{}}
	w := httptest.NewRecorder()
	Service{Host: h}.Capabilities(w, httptest.NewRequest("GET", "/v1/internal/hosting/capabilities", nil), state.Account{})
	var out api.HostingCapabilities
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || out.UploadBaseURL != h.HostingUploadBaseURL() ||
		out.AccountStorageMaxBytes != api.HostingAccountStorageMaxBytes || out.AccountPendingUploadsMax != api.HostingAccountPendingUploadsMax ||
		out.FilesMax != api.HostingFilesMax || out.FileMaxBytes != api.HostingFileMaxBytes ||
		out.ReleaseMaxBytes != api.HostingReleaseMaxBytes || out.ManifestMaxBytes != api.HostingManifestMaxBytes ||
		out.PathMaxBytes != api.HostingPathMaxBytes || out.ContentTypeMaxBytes != api.HostingContentTypeMaxBytes ||
		out.NameMaxBytes != api.HostingNameMaxBytes || out.SitesPerAccount != api.HostingSitesPerAccount ||
		out.ReleasesPerSite != api.HostingReleasesPerSite || out.UploadTTLSeconds != 300 ||
		out.StorageTimeoutSeconds != 30 || out.FinalizeTimeoutSeconds != 120 {
		t.Fatalf("capabilities: %d %s", w.Code, w.Body)
	}
	if h.artifacts.calls != 0 {
		t.Fatal("capability lookup reached provider")
	}
}
