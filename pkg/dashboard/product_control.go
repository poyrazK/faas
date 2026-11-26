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
	Age, ExecutionURL                                    string
	CanWaive, CanCredit, Active                          bool
}
type ProductControlDocument struct {
	ID, Kind, Amount, CreatedAt, Reference, OperationID, StatementID, Reason string
	// Exported statements show their provider invoice and payment status
	// instead of the manual handoff form (ADR-20261018212918).
	Exported, CanReviewExport         bool
	ExportProvider, ExportCustomer    string
	Invoice, InvoiceURL, DashboardURL string
}
type ProductControlComparison struct{ Feature, Before, After string }

// ProductControlOverdue is the app's overdue policy and the customers it
// holds (ADR-20261018212918). Owners change it with the CLI or API.
type ProductControlOverdue struct {
	Policy string
	Holds  []ProductControlOverdueHold
}
type ProductControlOverdueHold struct{ TenantID, DueAt, Since, URL, InvoiceURL string }

// The console renders safe projections only. Input/result payloads and session
// credentials are never placed in this view model.
type ProductControlData struct {
	AppSlug, BaseURL, TenantID, TenantName, TenantStatus, Period      string
	LatestRevision, Error, Notice, ReviewToken                        string
	Main                                                              bool
	Customer                                                          *api.ProductCustomer
	PlanSchedule                                                      *api.ProductPlanSchedule
	Customers                                                         []ProductControlCustomer
	Plans                                                             []ProductControlPlan
	Features                                                          []ProductControlFeature
	LatestFeatures                                                    []ProductControlFeature
	BillingQueue                                                      *ProductControlBillingQueue
	BillingBatch                                                      *ProductControlBillingBatch
	InvoiceBatch                                                      *ProductControlInvoiceBatch
	StripeSendBatch                                                   *ProductControlStripeSendBatch
	OutstandingBalances                                               *ProductControlOutstandingBalances
	BalancesReturnURL                                                 string
	PaymentQueue                                                      *ProductControlPaymentQueue
	Attention                                                         []ProductControlAttention
	Runs                                                              []ProductControlRun
	Documents                                                         []ProductControlDocument
	Billing                                                           *api.ProductBillingReview
	BillingAmount                                                     string
	NextCustomersURL, NextAttentionURL, NextRunsURL, NextDocumentsURL string
	PaymentQueueURL                                                   string
	InvoiceQueueURL                                                   string
	ReviewRunsURL, AllRunsURL                                         string
	ReviewRuns                                                        bool
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
	BulkPlans                                                         *ProductControlBulkPlans
	TeamPolicy                                                        *ProductControlTeamPolicy
	Help                                                              *ProductControlHelp
	Email                                                             *ProductControlEmail
	EmailRetry                                                        *api.ProductEmailRetryReceipt
	EmailRecoveries                                                   *ProductControlEmailRecoveries
	EmailOptOuts                                                      *ProductControlEmailOptOuts
	Overdue                                                           *ProductControlOverdue
	// CustomerHold explains an overdue-invoice hold, separate from Suspended.
	CustomerHold string
}

type ProductControlBillingQueue struct {
	Period, LatestPeriod, NextURL string
	Items                         []ProductControlBillingQueueItem
	CanReviewBatch                bool
	Invoices                      *ProductControlInvoiceQueue
}

type ProductControlBillingQueueItem struct {
	TenantID, Name, PlanFee, RunCharges, URL string
	Unresolved                               int64
}

type ProductControlBillingBatch struct {
	Period, QueueURL, InvoiceURL, Payload, Token string
	Rows                                         []ProductControlBillingBatchRow
	Ready                                        int
	Confirmed                                    bool
}

type ProductControlBillingBatchRow struct {
	TenantID, Name, PlanFee, RunCharges, Total, Status, URL string
}

type ProductControlStripeSendBatch struct {
	Period, Mode, QueueURL, Payload, Token, Error string
	Rows                                          []ProductControlStripeSendBatchRow
	Ready                                         int
	Confirmed                                     bool
}

type ProductControlStripeSendBatchRow struct {
	Name, TenantID, DocumentID, URL, InvoiceID, Recipient, Amount, DueAt, Status string
}

type ProductControlOutstandingBalances struct {
	Mode, Currency, Bucket, Cursor      string
	LiveURL, TestURL, SummaryURL, Group string
	RefreshPayload, RefreshToken        string
	RefreshLimit                        int
	RefreshResults                      []ProductControlBalanceRefreshResult
	BucketLabels                        []string
	Currencies                          []ProductControlBalanceCurrency
	Invoices                            *ProductControlPaymentQueue
}

type ProductControlBalanceRefreshResult struct {
	DocumentID, TenantID, Name, URL, InvoiceID, Status, Balance, Detail string
	NeedsRefresh                                                        bool
}

type ProductControlBalanceCurrency struct {
	Currency, Total            string
	InvoiceCount, UnknownCount int64
	Buckets                    []ProductControlBalanceBucket
}

type ProductControlBalanceBucket struct {
	URL, Balance                             string
	InvoiceCount, RefreshCount, UnknownCount int64
}

type ProductControlPaymentQueue struct {
	StripeBatchMonths              []string
	StripeBatchMax                 int
	Items                          []ProductControlPaymentQueueItem
	Cursor, NextURL, Notice, Error string
}

type ProductControlPaymentQueueItem struct {
	Name, TenantID, Period, DocumentID, Action, Remaining, DueAt, LastSync, DocumentURL, HostedURL string
	NeedsRefresh                                                                                   bool
	StripeReminderStatus                                                                           string
	CanSendStripeReminder                                                                          bool
	StripeDashboardURL                                                                             string
	CanIssue, CanRefresh, CanSendStripe, CanCheckStripeSend, CanBatchSendStripe                    bool
}

type ProductControlInvoiceQueue struct {
	DraftProviders []ProductControlInvoiceProvider
	Items          []ProductControlInvoiceQueueItem
	NextURL        string
}

type ProductControlInvoiceQueueItem struct {
	TenantID, DocumentID, Name, Amount, Provider, DocumentURL, ExportURL, HandoffURL string
}

type ProductControlInvoiceProvider struct {
	ID, Name string
}

type ProductControlInvoiceBatch struct {
	Period, Provider, QueueURL, PaymentURL, Payload, Token string
	Rows                                                   []ProductControlInvoiceBatchRow
	Ready                                                  int
	Confirmed                                              bool
}

type ProductControlInvoiceBatchRow struct {
	TenantID, DocumentID, Name, Customer, Subtotal, Mode, Status, URL, InvoiceID string
	AutomaticTax                                                                 bool
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
	SubmitLabel                        string
	ActionURL                          string
	Details                            []ProductControlComparison
}

type ProductControlBulkPlans struct {
	Plan, Source, Target, NextURL, Payload, Token string
	MaxCustomers                                  int
	Groups                                        []ProductControlBulkPlanGroup
	Reviews, Results                              []ProductControlBulkPlanRow
	ConfirmCount                                  int
}

type ProductControlBulkPlanGroup struct {
	Plan, Label, Source string
	Customers           []ProductControlCustomer
}

type ProductControlBulkPlanRow struct {
	TenantID, Name, Message, ReceiptID, EffectiveAt string
	Details                                         []ProductControlComparison
	Warnings                                        []string
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
