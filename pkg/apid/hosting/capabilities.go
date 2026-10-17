package hosting

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s Service) Capabilities(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.guard(w, acct) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	out := api.HostingCapabilities{
		StagedReleasesSupported: true,
		ConfigEnabled:           s.configEnabled(), ConfigMaxBytes: api.HostingConfigMaxBytes,
		ConfigRedirectsMax: api.HostingConfigRedirectsMax, ConfigHeaderRulesMax: api.HostingConfigHeaderRulesMax,
		ConfigHeadersPerRuleMax: api.HostingConfigHeadersPerRuleMax, ConfigHeaderValueMaxBytes: api.HostingConfigHeaderValueMaxBytes,
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
	s.retentionCapabilities(&out)
	s.extendCapabilities(r, &out)
	httpkit.WriteJSON(w, http.StatusOK, out)
}

func (s Service) extendCapabilities(r *http.Request, out *api.HostingCapabilities) {
	out.CancellationEnabled = s.cancellationEnabled()
	out.PreviewReservationsEnabled = s.previewReservationsEnabled()
	if h, ok := s.Host.(ActivityHost); ok && h.HostingActivityEnabled() && h.HostingActivityReader() != nil {
		if a, ok := s.Host.(AutomationHost); ok && a.HostingAutomationEnabled() && a.HostingAutomationStore() != nil {
			out.ActivityEnabled = true
			out.ActivityPageSize = api.HostingActivityPageSize
		}
	}

	if h, ok := s.Host.(CISetupHost); ok && h.HostingCISetupEnabled() {
		if o, ok := s.Host.(OIDCHost); ok && o.HostingOIDCStore() != nil {
			out.CISetupEnabled = true
		}
	}

	if h, ok := s.Host.(OIDCHost); ok && h.HostingOIDCEnabled() && h.HostingOIDCStore() != nil && s.OIDCVerifier != nil {
		if a, ok := s.Host.(AutomationHost); ok && a.HostingAutomationEnabled() && s.Host.HostingDurableRoutingEnabled() {
			out.OIDCEnabled = true
		}
	}
	if h, ok := s.Host.(DiagnosticsHost); ok && h.HostingDiagnosticsEnabled() && h.HostingDiagnosticReader() != nil {
		out.DiagnosticsEnabled = true
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
	if h, ok := s.Host.(PreviewHost); ok && h.HostingPreviewsEnabled() && s.Host.HostingDurableRoutingEnabled() && h.HostingPreviewStore() != nil {
		out.PreviewsEnabled = true
		out.PreviewsPerSite = api.HostingPreviewsPerSite
		out.PreviewTTLDefaultSeconds = int64(api.HostingPreviewTTLDefault / time.Second)
		out.PreviewTTLMinSeconds = int64(api.HostingPreviewTTLMin / time.Second)
		out.PreviewTTLMaxSeconds = int64(api.HostingPreviewTTLMax / time.Second)
	}
	if h, ok := s.Host.(HistoryHost); ok && h.HostingHistoryEnabled() && h.HostingHistoryStore() != nil {
		out.HistoryEnabled = true
		out.HistoryPerSite = api.HostingHistoryPerSite
		out.HistoryPageSize = api.HostingHistoryPageSize
		out.ProvenanceRepositoryMaxBytes = api.HostingProvenanceRepositoryMaxBytes
		out.ProvenanceBranchMaxBytes = api.HostingProvenanceBranchMaxBytes
		out.ProvenanceDescriptionMaxBytes = api.HostingProvenanceDescriptionMaxBytes
	}
	if h, ok := s.Host.(AutomationHost); ok && h.HostingAutomationEnabled() && h.HostingAutomationStore() != nil {
		out.AutomationEnabled = true
		out.AutomationCredentialsPerSite = api.HostingAutomationCredentialsPerSite
		out.AutomationTTLMaxSeconds = int64(api.HostingAutomationTTLMax / time.Second)
		out.CIRunsPerSite = api.HostingCIRunsPerSite
	}
	if principal, ok := httpkit.PrincipalFrom(r); ok && principal.Key != nil && principal.Key.HostingSiteID != "" {
		out.AutomationSiteID = principal.Key.HostingSiteID
		out.AutomationOperations = append([]string(nil), principal.Key.HostingOperations...)
	}
}

func (s Service) retentionCapabilities(out *api.HostingCapabilities) {
	if h, ok := s.Host.(RetentionHost); ok && h.HostingRetentionEnabled() && h.HostingRetentionStore() != nil {
		out.RetentionEnabled = true
		out.RetentionAgeDefaultSeconds = int64(api.HostingRetentionAgeDefault / time.Second)
		out.RetentionAgeMinSeconds = int64(api.HostingRetentionAgeMin / time.Second)
		out.RetentionAgeMaxSeconds = int64(api.HostingRetentionAgeMax / time.Second)
	}
}
