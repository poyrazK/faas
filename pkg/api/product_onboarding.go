package api

import "time"

// Onboarding connects an existing Application Auth workspace. Credentials are
// transient request fields and never part of a preview or retained receipt.
type ProductOnboardingWorkspace struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type ProductOnboardingWorkspaces struct {
	AuthApp    string                       `json:"auth_app"`
	AuthAppID  string                       `json:"auth_app_id"`
	Workspaces []ProductOnboardingWorkspace `json:"workspaces"`
}

type ProductOnboardingRequest struct {
	ID               string `json:"id"`
	TenantID         string `json:"tenant_id,omitempty"`
	ExternalRef      string `json:"external_ref,omitempty"`
	Name             string `json:"name,omitempty"`
	Plan             string `json:"plan"`
	Revision         string `json:"revision"`
	AuthApp          string `json:"auth_app"`
	AuthAppID        string `json:"auth_app_id"`
	OrganizationID   string `json:"organization_id"`
	OrganizationName string `json:"organization_name"`
}

type ProductOnboardingTenant struct {
	ID          string `json:"id,omitempty"`
	ExternalRef string `json:"external_ref"`
	Name        string `json:"name"`
	Status      string `json:"status"`
}

type ProductOnboardingPreview struct {
	Request       ProductOnboardingRequest `json:"request"`
	Tenant        ProductOnboardingTenant  `json:"tenant"`
	CreatesTenant bool                     `json:"creates_tenant"`
	Before        *ProductCustomer         `json:"before,omitempty"`
	Catalog       ProductCatalog           `json:"catalog"`
	HeadRevision  string                   `json:"head_revision"`
	Period        string                   `json:"period"`
	Hash          string                   `json:"hash"`
}

type ProductOnboardingConfirmation struct {
	Request      ProductOnboardingRequest `json:"request"`
	ExpectedHash string                   `json:"expected_hash"`
}

type ProductOnboardingAuthRequest struct {
	AuthAccessToken string `json:"auth_access_token"`
}

type ProductOnboardingReviewRequest struct {
	Request         ProductOnboardingRequest `json:"request"`
	AuthAccessToken string                   `json:"auth_access_token"`
}

type ProductOnboardingApplyRequest struct {
	Confirmation    ProductOnboardingConfirmation `json:"confirmation"`
	AuthAccessToken string                        `json:"auth_access_token,omitempty"`
}

type ProductOnboardingReceipt struct {
	ID            string                   `json:"id"`
	PreviewHash   string                   `json:"preview_hash"`
	Request       ProductOnboardingRequest `json:"request"`
	Tenant        ProductOnboardingTenant  `json:"tenant"`
	CreatedTenant bool                     `json:"created_tenant"`
	Customer      ProductCustomer          `json:"customer"`
	CreatedAt     time.Time                `json:"created_at"`
}

type ProductOnboardingSuccess struct {
	OperationID string    `json:"operation_id"`
	Feature     string    `json:"feature"`
	VerifiedAt  time.Time `json:"verified_at"`
}

type ProductOnboardingStatus struct {
	Receipt          ProductOnboardingReceipt  `json:"receipt"`
	CurrentCustomer  *ProductCustomer          `json:"current_customer,omitempty"`
	SetupCurrent     bool                      `json:"setup_current"`
	AccessConfigured bool                      `json:"access_configured"`
	AccessVerifiedAt *time.Time                `json:"access_verified_at,omitempty"`
	FirstSuccess     *ProductOnboardingSuccess `json:"first_success,omitempty"`
	PortalPath       string                    `json:"portal_path"`
	AuthSignInURL    string                    `json:"auth_sign_in_url,omitempty"`
}
