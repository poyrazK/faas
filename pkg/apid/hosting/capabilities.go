package hosting

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s Service) Capabilities(w http.ResponseWriter, _ *http.Request, acct state.Account) {
	if !s.guard(w, acct) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, api.HostingCapabilities{
		UploadBaseURL: s.Host.HostingUploadBaseURL(),
		FilesMax:      api.HostingFilesMax, FileMaxBytes: api.HostingFileMaxBytes,
		ReleaseMaxBytes: api.HostingReleaseMaxBytes, ManifestMaxBytes: api.HostingManifestMaxBytes,
		PathMaxBytes: api.HostingPathMaxBytes, ContentTypeMaxBytes: api.HostingContentTypeMaxBytes,
		NameMaxBytes: api.HostingNameMaxBytes, SitesPerAccount: api.HostingSitesPerAccount,
		ReleasesPerSite:        api.HostingReleasesPerSite,
		UploadTTLSeconds:       int(api.HostingUploadTTL / time.Second),
		StorageTimeoutSeconds:  int(api.HostingStorageTimeout / time.Second),
		FinalizeTimeoutSeconds: int(api.HostingFinalizeTimeout / time.Second),
	})
}
