package api

import "time"

type HostingDNSRecord struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

type HostingDomainDetails struct {
	CreateStartedAt     *time.Time         `json:"create_started_at,omitempty"`
	ValidationRetriedAt *time.Time         `json:"validation_retried_at,omitempty"`
	ProviderID          string             `json:"provider_id,omitempty"`
	AccessID            string             `json:"access_id,omitempty"`
	AccessAUD           string             `json:"access_aud,omitempty"`
	TLSStatus           string             `json:"tls_status"`
	Records             []HostingDNSRecord `json:"records"`
	VerifiedAt          *time.Time         `json:"verified_at,omitempty"`
}

type HostingDomain struct {
	ID                  string               `json:"id"`
	AccountID           string               `json:"account_id"`
	SiteID              string               `json:"site_id"`
	Hostname            string               `json:"hostname"`
	ProviderOriginID    string               `json:"provider_origin_id"`
	Challenge           string               `json:"challenge"`
	State               string               `json:"state"`
	Revision            int64                `json:"revision"`
	Generation          int64                `json:"generation"`
	PublishedGeneration int64                `json:"published_generation"`
	PublishedAt         *time.Time           `json:"published_at,omitempty"`
	Details             HostingDomainDetails `json:"details"`
	NextAttemptAt       time.Time            `json:"next_attempt_at"`
	Attempts            int64                `json:"attempts"`
	LastErrorCode       string               `json:"last_error_code,omitempty"`
	CreatedAt           time.Time            `json:"created_at"`
}

type CreateHostingDomainRequest struct {
	Hostname string `json:"hostname"`
}
type DeleteHostingDomainRequest struct {
	ExpectedRevision int64 `json:"expected_revision"`
}

type RecoverHostingDomainRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	ProviderID       string `json:"provider_id"`
	ExpectedHostname string `json:"expected_hostname"`
}
