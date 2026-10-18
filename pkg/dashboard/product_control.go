package dashboard

import "github.com/onebox-faas/faas/pkg/api"

type ProductControlCustomer struct {
	TenantID, Name, Status, Plan, Revision, URL string
	Suspended                                   bool
}

type ProductControlFeature struct {
	Name, Label, Price, Binding string
	Enabled                     bool
	Limit, Used, Remaining      int64
}

type ProductControlPlan struct{ Name, Label string }
type ProductControlAttention struct {
	TenantID, Period, URL   string
	Unresolved, NeedsReview int64
}
type ProductControlRun struct {
	ID, Feature, AdmittedAt, State, Price, Credited, URL string
	CanWaive, CanCredit                                  bool
}
type ProductControlDocument struct {
	ID, Kind, Amount, CreatedAt, Reference, OperationID, StatementID, Reason string
}
type ProductControlComparison struct{ Feature, Before, After string }

// The console renders safe projections only. Input/result payloads and session
// credentials are never placed in this view model.
type ProductControlData struct {
	AppSlug, BaseURL, TenantID, TenantName, TenantStatus, Period      string
	LatestRevision, Error, Notice, ReviewToken                        string
	Main                                                              bool
	Customer                                                          *api.ProductCustomer
	Customers                                                         []ProductControlCustomer
	Plans                                                             []ProductControlPlan
	Features                                                          []ProductControlFeature
	LatestFeatures                                                    []ProductControlFeature
	Attention                                                         []ProductControlAttention
	Runs                                                              []ProductControlRun
	Documents                                                         []ProductControlDocument
	Billing                                                           *api.ProductBillingReview
	BillingAmount                                                     string
	NextCustomersURL, NextAttentionURL, NextRunsURL, NextDocumentsURL string
	Confirmation                                                      *ProductControlConfirmation
	Explanation                                                       *api.ProductOperationExplanation
	NextExecutionURL                                                  string
	Setup                                                             *ProductControlSetup
	Doctor                                                            *api.ProductDoctorReport
	Overview                                                          *ProductControlOverview
	Activity                                                          *ProductControlActivity
	Onboarding                                                        *ProductControlOnboarding
	OnboardingStatus                                                  *api.ProductOnboardingStatus
	Access                                                            *ProductControlAccess
	PlanPolicy                                                        *ProductControlPlanPolicy
	TeamPolicy                                                        *ProductControlTeamPolicy
	Help                                                              *ProductControlHelp
	Email                                                             *ProductControlEmail
	EmailRetry                                                        *api.ProductEmailRetryReceipt
	EmailRecoveries                                                   *ProductControlEmailRecoveries
	EmailOptOuts                                                      *ProductControlEmailOptOuts
}

type ProductControlEmail struct {
	Report                     api.ProductEmailOwnerPage
	Deliveries                 []ProductControlEmailRow
	NextURL, BacklogAge, Token string
}
type ProductControlEmailRow struct {
	api.ProductEmailOwnerDelivery
	Token                                string
	StatusLabel, ReasonLabel, RetryLabel string
}

type ProductControlHelp struct {
	Queue              []ProductControlHelpRow
	Request            *api.ProductHelpRequest
	Status, NextURL    string
	ContextJSON, Token string
	TextMaxBytes       int
}

type ProductControlHelpRow struct {
	api.ProductHelpRequest
	URL, Preview string
}

type ProductControlActivity struct {
	Report  api.ProductCustomerActivityPage
	Events  []ProductControlActivityEvent
	NextURL string
}

type ProductControlActivityEvent struct {
	api.ProductCustomerActivityEvent
	Label, Summary, SourceJSON string
}

type ProductControlOverview struct {
	Report    api.ProductCustomerOverviewPage
	Customers []ProductControlOverviewCustomer
	NextURL   string
}

type ProductControlOverviewCustomer struct {
	api.ProductCustomerOverview
	ActivationLabel, ConfigurationLabel, URL string
}

type ProductControlTeamPolicy struct {
	Revision, ReviewToken string
	Roles                 []string
	Delegations           []ProductControlTeamDelegation
}
type ProductControlTeamDelegation struct{ Actor, Target string }

type ProductControlPlanPolicy struct {
	Revision, ReviewToken string
	Plans                 []ProductControlPlan
	Transitions           []ProductControlPlanTransition
}
type ProductControlPlanTransition struct{ Source, Target string }

type ProductControlAccess struct {
	Revision, AuthApp, ReviewToken string
	Default                        bool
	Roles                          []ProductControlAccessRole
}

type ProductControlAccessRole struct {
	Name string
	api.ProductPermissions
}

type ProductControlOnboarding struct {
	ID, ReviewToken, SessionToken, TenantID, AuthSignInURL string
	Connected                                              bool
	Workspaces                                             []api.ProductOnboardingWorkspace
	Tenants                                                []ProductControlCustomer
	NextTenantsURL                                         string
}

type ProductControlConfirmation struct {
	Title, Description, Payload, Token string
	ActionURL                          string
	Details                            []ProductControlComparison
}

type ProductControlSetup struct {
	MaxStarts, MaxPrice                                    int64
	AuthApp, Currency, Feature, Label, Operation, Scope    string
	StarterName, StarterLabel, StarterStarts, StarterPrice string
	ProName, ProLabel, ProStarts, ProPrice                 string
	ReviewToken                                            string
	Apps                                                   []string
	Tenants                                                []ProductControlCustomer
	NextTenantsURL                                         string
	CatalogJSON                                            string
	ChooseCustomer                                         bool
}

type ProductControlEmailRecoveries struct {
	Page    api.ProductEmailRecoveryPage
	NextURL string
}
type ProductControlEmailOptOuts struct {
	Page    api.ProductEmailOptOutPage
	NextURL string
}
