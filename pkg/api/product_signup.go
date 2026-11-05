package api

// ProductSignupPolicy lets visitors sign up through the customer portal
// (ADR-20261022100735): the plans offered at signup and, optionally, the
// billing provider whose customer each signup creates.
type ProductSignupPolicy struct {
	Plans    []string `json:"plans"`
	Provider string   `json:"provider,omitempty"`
}

// ProductSignupOffer is a product's public signup offer: the offered plans'
// terms and the Application Auth sign-up page. It names no account.
type ProductSignupOffer struct {
	Product   string             `json:"product"`
	Currency  string             `json:"currency"`
	Plans     []ProductPlanTerms `json:"plans"`
	Provider  string             `json:"provider,omitempty"`
	SignUpURL string             `json:"sign_up_url"`
}

// ProductSignupRequest creates the signed-in user's workspace on an offered
// plan. ID is the client's idempotency UUID.
type ProductSignupRequest struct {
	ID            string `json:"id"`
	Plan          string `json:"plan"`
	WorkspaceName string `json:"workspace_name"`
}

// ProductSignupReceipt names the workspace a signup created.
type ProductSignupReceipt struct {
	OrganizationID string          `json:"organization_id"`
	Workspace      string          `json:"workspace"`
	Customer       ProductCustomer `json:"customer"`
}

const (
	// CodeProductSignupUnavailable refuses signup when the product does not
	// offer it or operators paused it.
	CodeProductSignupUnavailable = "product_signup_unavailable"
	// CodeProductSignupLimit refuses a user's workspace beyond
	// ProductControlSignupMaxWorkspacesPerUser.
	CodeProductSignupLimit = "product_signup_limit"
	// CodeProductPaymentMethodUnavailable refuses card collection for a
	// workspace not billed through the product team's Stripe.
	CodeProductPaymentMethodUnavailable = "product_payment_method_unavailable"
)

// ProductPaymentMethod summarizes the card the provider keeps as a
// workspace's default for invoices (ADR-20261022100735); Gregale stores no
// card data.
type ProductPaymentMethod struct {
	Provider string `json:"provider"`
	Brand    string `json:"brand"`
	Last4    string `json:"last4"`
	ExpMonth int    `json:"exp_month"`
	ExpYear  int    `json:"exp_year"`
}

// ProductPaymentMethodCheckoutRequest opens the provider's hosted card
// page. Consent must be true: the customer agrees that the product team
// charges the saved card for future invoices.
type ProductPaymentMethodCheckoutRequest struct {
	Consent bool `json:"consent"`
}

// ProductPaymentMethodCheckout is the provider page to open.
type ProductPaymentMethodCheckout struct {
	URL string `json:"url"`
}

// ProductPaymentMethodCompleteRequest finishes a returned Checkout.
type ProductPaymentMethodCompleteRequest struct {
	SessionID string `json:"session_id"`
}

// ProductPaymentMethodView is a workspace's card on file, nil without one.
type ProductPaymentMethodView struct {
	Available     bool                  `json:"available"`
	PaymentMethod *ProductPaymentMethod `json:"payment_method,omitempty"`
}
