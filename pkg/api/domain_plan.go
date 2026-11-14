package api

// DomainManifest declares only the listed bindings. Omission never detaches a
// domain. An empty environment selects the app's ordinary production routing.
type DomainManifest struct {
	Version int                 `json:"version,omitempty" yaml:"version"`
	Domains []DomainBindingSpec `json:"domains,omitempty" yaml:"domains"`
}

type DomainBindingSpec struct {
	Domain      string `json:"domain,omitempty" yaml:"domain"`
	App         string `json:"app,omitempty" yaml:"app"`
	Environment string `json:"environment,omitempty" yaml:"environment,omitempty"`
}

type DomainBindingTarget struct {
	App           string `json:"app,omitempty"`
	AppID         string `json:"app_id,omitempty"`
	Environment   string `json:"environment,omitempty"`
	EnvironmentID string `json:"environment_id,omitempty"`
}

type DomainBindingChange struct {
	Domain        string               `json:"domain,omitempty"`
	Action        string               `json:"action,omitempty"`
	Before        *DomainBindingTarget `json:"before,omitempty"`
	After         DomainBindingTarget  `json:"after,omitempty"`
	Verified      bool                 `json:"verified,omitempty"`
	ClearsDefault bool                 `json:"clears_default,omitempty"`
	DeploymentIDs []string             `json:"deployment_ids,omitempty"`
}

type DomainPlan struct {
	Version  int                   `json:"version,omitempty"`
	SHA256   string                `json:"sha256,omitempty"`
	Manifest DomainManifest        `json:"manifest,omitempty"`
	Changes  []DomainBindingChange `json:"changes,omitempty"`
}

type DomainApplyRequest struct {
	Manifest           DomainManifest `json:"manifest,omitempty"`
	ExpectedPlanSHA256 string         `json:"expected_plan_sha256,omitempty"`
	Confirm            bool           `json:"confirm,omitempty"`
}

type DomainApplyResponse struct {
	Plan    DomainPlan             `json:"plan,omitempty"`
	Domains []CustomDomainResponse `json:"domains,omitempty"`
	// Rollback restores previous bindings through another reviewed plan. Newly
	// created claims and previous default-domain selections require explicit care.
	Rollback *DomainManifest `json:"rollback,omitempty"`
}
