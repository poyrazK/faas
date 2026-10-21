package api

// Product permissions apply to Application Auth customers. Platform tenant
// service credentials continue to use their explicit platform scopes.
type ProductPermission string

const (
	ProductPermissionViewWork    ProductPermission = "work:read"
	ProductPermissionRunWork     ProductPermission = "work:run"
	ProductPermissionViewBilling ProductPermission = "billing:read"
	ProductPermissionManagePlan  ProductPermission = "plans:manage"
	ProductPermissionManageTeam  ProductPermission = "team:manage"
	// ProductPermissionManageBilling changes the workspace's billing
	// details in the owner's provider (ADR-20261020120000).
	ProductPermissionManageBilling ProductPermission = "billing:manage"
)

type ProductAccessPolicy struct {
	Roles map[string][]ProductPermission `json:"roles"`
}

// RunWork includes ViewWork so operators can follow and recover their work.
type ProductPermissions struct {
	ViewWork    bool `json:"view_work"`
	RunWork     bool `json:"run_work"`
	ViewBilling bool `json:"view_billing"`
	ManagePlan  bool `json:"manage_plan"`
	ManageTeam  bool `json:"manage_team"`
	// ManageBilling is omitted when false, so permission sets without it
	// encode exactly as before in stored receipts and hashes.
	ManageBilling bool `json:"manage_billing,omitempty"`
}

type ProductCustomerAccess struct {
	Role           string             `json:"role"`
	PolicyRevision string             `json:"policy_revision"`
	Permissions    ProductPermissions `json:"permissions"`
}

type ProductAccessConfiguration struct {
	Revision string              `json:"revision"`
	AuthApp  string              `json:"auth_app"`
	Default  bool                `json:"default"`
	Policy   ProductAccessPolicy `json:"policy"`
}

type ProductAccessChangeRequest struct {
	ExpectedHead     string              `json:"expected_head"`
	ExpectedRevision string              `json:"expected_revision"`
	Policy           ProductAccessPolicy `json:"policy"`
}

type ProductAccessChangePreview struct {
	Before ProductAccessConfiguration `json:"before"`
	After  ProductAccessConfiguration `json:"after"`
	Change ProductAccessChangeRequest `json:"change"`
}
