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
	out := api.HostingCapabilities{
		AccountStorageMaxBytes: api.HostingAccountStorageMaxBytes, AccountPendingUploadsMax: api.HostingAccountPendingUploadsMax,
		UploadBaseURL: s.Host.HostingUploadBaseURL(),
		FilesMax:      api.HostingFilesMax, FileMaxBytes: api.HostingFileMaxBytes,
		ReleaseMaxBytes: api.HostingReleaseMaxBytes, ManifestMaxBytes: api.HostingManifestMaxBytes,
		PathMaxBytes: api.HostingPathMaxBytes, ContentTypeMaxBytes: api.HostingContentTypeMaxBytes,
		NameMaxBytes: api.HostingNameMaxBytes, SitesPerAccount: api.HostingSitesPerAccount,
		ReleasesPerSite:        api.HostingReleasesPerSite,
		UploadTTLSeconds:       int(api.HostingUploadTTL / time.Second),
		StorageTimeoutSeconds:  int(api.HostingStorageTimeout / time.Second),
		FinalizeTimeoutSeconds: int(api.HostingFinalizeTimeout / time.Second),
	}
	if h, ok := s.Host.(AnalyticsHost); ok && h.HostingAnalyticsEnabled() && h.HostingTrafficReader() != nil {
		out.AnalyticsEnabled = true
	}
	if h, ok := s.Host.(DomainHost); ok && h.HostingDomainsEnabled() && h.HostingDomainProvider() != nil && h.HostingDomainStore() != nil {
		out.DomainsEnabled = true
		out.DomainsPerSite = api.HostingDomainsPerSite
		out.DomainHostnameMaxBytes = api.HostingDomainHostnameMaxBytes
		out.DomainCNAMETarget = h.HostingDomainProvider().Target()
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}
