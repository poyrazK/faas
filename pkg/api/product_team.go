package api

import (
	"encoding/json"
	"time"
)

// Delegations explicitly name which existing Auth roles each team manager may
// assign, change or remove. Omitted/empty is closed; it grants no permission.
type ProductTeamPolicy struct {
	Delegations map[string][]string `json:"delegations"`
}
type ProductTeamPolicyConfiguration struct {
	Revision string            `json:"revision"`
	Policy   ProductTeamPolicy `json:"policy"`
}
type ProductTeamPolicyChange struct {
	ExpectedHead     string            `json:"expected_head"`
	ExpectedRevision string            `json:"expected_revision"`
	Policy           ProductTeamPolicy `json:"policy"`
}
type ProductTeamPolicyPreview struct {
	Before ProductTeamPolicyConfiguration `json:"before"`
	After  ProductTeamPolicyConfiguration `json:"after"`
	Change ProductTeamPolicyChange        `json:"change"`
}

type ProductTeamMember struct {
	UserID      string             `json:"user_id"`
	Email       string             `json:"email"`
	Role        string             `json:"role"`
	CreatedAt   time.Time          `json:"created_at"`
	Permissions ProductPermissions `json:"product_permissions"`
	CanManage   bool               `json:"can_manage"`
}
type ProductTeamInvitation struct {
	ID          string             `json:"id"`
	Email       string             `json:"email"`
	Role        string             `json:"role"`
	ExpiresAt   time.Time          `json:"expires_at"`
	CreatedAt   time.Time          `json:"created_at"`
	Permissions ProductPermissions `json:"product_permissions"`
	CanRevoke   bool               `json:"can_revoke"`
}
type ProductTeamRole struct {
	Role            string             `json:"role"`
	Permissions     ProductPermissions `json:"product_permissions"`
	AuthPermissions []string           `json:"workspace_permissions"`
}
type ProductTeamView struct {
	ActorID                  string                  `json:"actor_id"`
	ActorRole                string                  `json:"actor_role"`
	Revision                 string                  `json:"revision"`
	Members                  []ProductTeamMember     `json:"members"`
	Invitations              []ProductTeamInvitation `json:"invitations"`
	AssignableRoles          []ProductTeamRole       `json:"assignable_roles"`
	Suspended                bool                    `json:"suspended"`
	InvitationDefaultSeconds int                     `json:"invitation_default_seconds"`
}

type ProductTeamActionRequest struct {
	ID        string `json:"id"`
	Action    string `json:"action"`
	TargetID  string `json:"target_id,omitempty"`
	Email     string `json:"email,omitempty"`
	Role      string `json:"role,omitempty"`
	ExpiresIn int    `json:"expires_in,omitempty"`
}
type ProductTeamActionPreview struct {
	Request                    ProductTeamActionRequest `json:"request"`
	ActorID                    string                   `json:"actor_id"`
	ActorRole                  string                   `json:"actor_role"`
	Subject                    string                   `json:"subject"`
	BeforeRole                 string                   `json:"before_role"`
	AfterRole                  string                   `json:"after_role"`
	BeforePermissions          ProductPermissions       `json:"before_permissions"`
	AfterPermissions           ProductPermissions       `json:"after_permissions"`
	BeforeWorkspacePermissions []string                 `json:"before_workspace_permissions"`
	AfterWorkspacePermissions  []string                 `json:"after_workspace_permissions"`
	Revision                   string                   `json:"revision"`
	AuthSnapshot               string                   `json:"auth_snapshot"`
	Hash                       string                   `json:"hash"`
}
type ProductTeamActionConfirmation struct {
	Request      ProductTeamActionRequest `json:"request"`
	AuthSnapshot string                   `json:"auth_snapshot"`
	ExpectedHash string                   `json:"expected_hash"`
}

// Auth owns this atomic receipt. Context is caller-provided correlation only;
// it never grants authority. Credentials and invitation tokens are excluded.
type ProductTeamActionReceipt struct {
	ReviewHash     string          `json:"review_hash,omitempty"`
	ID             string          `json:"id"`
	OrganizationID string          `json:"org_id"`
	ActorID        string          `json:"actor_id"`
	ActorRole      string          `json:"actor_role"`
	Action         string          `json:"action"`
	Request        json.RawMessage `json:"request"`
	Result         json.RawMessage `json:"result"`
	CreatedAt      time.Time       `json:"created_at"`
}
type ProductTeamActionResult struct {
	Receipt       ProductTeamActionReceipt `json:"receipt"`
	InvitationURL string                   `json:"invitation_url,omitempty"`
	Replayed      bool                     `json:"replayed"`
}
type ProductTeamActionHistory struct {
	Actions    []ProductTeamActionReceipt `json:"actions"`
	NextCursor string                     `json:"next_cursor,omitempty"`
}
