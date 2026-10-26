package internalmail

import (
	"errors"
	"net"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	EnableEnv                         = "FAAS_INTERNAL_MAIL_ENABLED"
	DeliveryEnableEnv                 = "FAAS_INTERNAL_MAIL_DELIVERY_ENABLED"
	ReportsEnableEnv                  = "FAAS_INTERNAL_MAIL_REPORTS_ENABLED"
	FeedbackEnableEnv                 = "FAAS_INTERNAL_MAIL_FEEDBACK_ENABLED"
	RetentionEnableEnv                = "FAAS_INTERNAL_MAIL_RETENTION_ENABLED"
	OperationsEnableEnv               = "FAAS_INTERNAL_MAIL_OPERATIONS_ENABLED"
	RotationEnableEnv                 = "FAAS_INTERNAL_MAIL_DKIM_ROTATION_ENABLED"
	ReverifyEnableEnv                 = "FAAS_INTERNAL_MAIL_DOMAIN_REVERIFY_ENABLED"
	ControlsEnableEnv                 = "FAAS_INTERNAL_MAIL_DELIVERY_CONTROLS_ENABLED"
	ProducersEnableEnv                = "FAAS_INTERNAL_MAIL_PRODUCERS_ENABLED"
	SchedulingEnableEnv               = "FAAS_INTERNAL_MAIL_SCHEDULING_ENABLED"
	PacingEnableEnv                   = "FAAS_INTERNAL_MAIL_DOMAIN_PACING_ENABLED"
	BackpressureEnableEnv             = "FAAS_INTERNAL_MAIL_RELAY_BACKPRESSURE_ENABLED"
	ReputationEnableEnv               = "FAAS_INTERNAL_MAIL_REPUTATION_ENABLED"
	EventFeedEnableEnv                = "FAAS_INTERNAL_MAIL_EVENT_FEED_ENABLED"
	ProducerSchedulingEnableEnv       = "FAAS_INTERNAL_MAIL_PRODUCER_SCHEDULING_ENABLED"
	ProducerLifecycleEnableEnv        = "FAAS_INTERNAL_MAIL_PRODUCER_LIFECYCLE_ENABLED"
	EncryptionRotationEnableEnv       = "FAAS_INTERNAL_MAIL_ENCRYPTION_ROTATION_ENABLED"
	SuppressionControlsEnableEnv      = "FAAS_INTERNAL_MAIL_SUPPRESSION_CONTROLS_ENABLED"
	ProducerOutboxEnableEnv           = "FAAS_INTERNAL_MAIL_PRODUCER_OUTBOX_ENABLED"
	OutboxReconciliationEnableEnv     = "FAAS_INTERNAL_MAIL_OUTBOX_RECONCILIATION_ENABLED"
	VerificationOutboxEnableEnv       = "FAAS_INTERNAL_MAIL_VERIFICATION_OUTBOX_ENABLED"
	PasswordResetOutboxEnableEnv      = "FAAS_INTERNAL_MAIL_PASSWORD_RESET_OUTBOX_ENABLED"
	MagicLinkOutboxEnableEnv          = "FAAS_INTERNAL_MAIL_MAGIC_LINK_OUTBOX_ENABLED"
	MFADisableOutboxEnableEnv         = "FAAS_INTERNAL_MAIL_MFA_DISABLE_OUTBOX_ENABLED"
	DeploymentFailedOutboxEnableEnv   = "FAAS_INTERNAL_MAIL_DEPLOYMENT_FAILED_OUTBOX_ENABLED"
	SubscriptionEndedOutboxEnableEnv  = "FAAS_INTERNAL_MAIL_SUBSCRIPTION_ENDED_OUTBOX_ENABLED"
	QuotaWarningOutboxEnableEnv       = "FAAS_INTERNAL_MAIL_QUOTA_WARNING_OUTBOX_ENABLED"
	NonpaymentDeletionOutboxEnableEnv = "FAAS_INTERNAL_MAIL_NONPAYMENT_DELETION_OUTBOX_ENABLED"
	AccountSuspendedOutboxEnableEnv   = "FAAS_INTERNAL_MAIL_ACCOUNT_SUSPENDED_OUTBOX_ENABLED"
	// ManagedPostgresCapOutboxEnableEnv sends the managed PostgreSQL compute
	// cap warnings (ADR-20261025094915); off by default.
	ManagedPostgresCapOutboxEnableEnv   = "FAAS_INTERNAL_MAIL_MANAGED_POSTGRES_CAP_OUTBOX_ENABLED"
	AccountRestoredOutboxEnableEnv      = "FAAS_INTERNAL_MAIL_ACCOUNT_RESTORED_OUTBOX_ENABLED"
	PaymentFailedOutboxEnableEnv        = "FAAS_INTERNAL_MAIL_PAYMENT_FAILED_OUTBOX_ENABLED"
	MFAEnabledAlertOutboxEnableEnv      = "FAAS_INTERNAL_MAIL_MFA_ENABLED_ALERT_OUTBOX_ENABLED"
	PasswordChangedAlertOutboxEnableEnv = "FAAS_INTERNAL_MAIL_PASSWORD_CHANGED_ALERT_OUTBOX_ENABLED"
	MFADisabledAlertOutboxEnableEnv     = "FAAS_INTERNAL_MAIL_MFA_DISABLED_ALERT_OUTBOX_ENABLED"
	MFARecoveryAlertOutboxEnableEnv     = "FAAS_INTERNAL_MAIL_MFA_RECOVERY_ALERT_OUTBOX_ENABLED"
	RecoveryEnableEnv                   = "FAAS_INTERNAL_MAIL_RECOVERY_ENABLED"
	RecoveryIDEnv                       = "FAAS_INTERNAL_MAIL_RECOVERY_ID"
	DefaultSocket                       = "/run/faas/maild.sock"
	APIPath                             = "/internal/v1/mail"
)

// Enabled deliberately accepts only an explicit 1. An unset, malformed or
// merely truthy value cannot accidentally launch a new mail service.
func Enabled(getenv func(string) string) bool { return getenv(EnableEnv) == "1" }

func ReportsEnabled(getenv func(string) string) bool {
	return Enabled(getenv) && getenv(ReportsEnableEnv) == "1"
}

func FeedbackEnabled(getenv func(string) string) bool {
	return Enabled(getenv) && getenv(FeedbackEnableEnv) == "1"
}

func RetentionEnabled(getenv func(string) string) bool {
	return Enabled(getenv) && getenv(RetentionEnableEnv) == "1"
}

func OperationsEnabled(getenv func(string) string) bool {
	return Enabled(getenv) && getenv(OperationsEnableEnv) == "1"
}

func RotationEnabled(getenv func(string) string) bool {
	return Enabled(getenv) && getenv(RotationEnableEnv) == "1"
}
func ReverificationEnabled(getenv func(string) string) bool {
	return Enabled(getenv) && getenv(ReverifyEnableEnv) == "1"
}

func DeliveryControlsEnabled(getenv func(string) string) bool {
	return Enabled(getenv) && getenv(ControlsEnableEnv) == "1"
}

func ProducersEnabled(getenv func(string) string) bool {
	return Enabled(getenv) && getenv(ProducersEnableEnv) == "1"
}

func SchedulingEnabled(getenv func(string) string) bool {
	return Enabled(getenv) && getenv(SchedulingEnableEnv) == "1"
}

func PacingEnabled(getenv func(string) string) bool {
	return Enabled(getenv) && getenv(PacingEnableEnv) == "1"
}

func BackpressureEnabled(getenv func(string) string) bool {
	return Enabled(getenv) && getenv(BackpressureEnableEnv) == "1"
}

func ReputationEnabled(getenv func(string) string) bool {
	return Enabled(getenv) && getenv(ReputationEnableEnv) == "1"
}

func EventFeedEnabled(getenv func(string) string) bool {
	return Enabled(getenv) && getenv(EventFeedEnableEnv) == "1"
}

func ProducerSchedulingEnabled(getenv func(string) string) bool {
	return Enabled(getenv) && getenv(ProducerSchedulingEnableEnv) == "1"
}

func ProducerLifecycleEnabled(getenv func(string) string) bool {
	return Enabled(getenv) && getenv(ProducerLifecycleEnableEnv) == "1"
}

func EncryptionRotationEnabled(getenv func(string) string) bool {
	return Enabled(getenv) && getenv(EncryptionRotationEnableEnv) == "1"
}

func SuppressionControlsEnabled(getenv func(string) string) bool {
	return Enabled(getenv) && getenv(SuppressionControlsEnableEnv) == "1"
}

func ProducerOutboxEnabled(getenv func(string) string) bool {
	return Enabled(getenv) && getenv(ProducerOutboxEnableEnv) == "1"
}

func OutboxReconciliationEnabled(getenv func(string) string) bool {
	return getenv(OutboxReconciliationEnableEnv) == "1" && ProducerOutboxEnabled(getenv)
}

func VerificationOutboxEnabled(getenv func(string) string) bool {
	return getenv(VerificationOutboxEnableEnv) == "1" && ProducerOutboxEnabled(getenv)
}

func PasswordResetOutboxEnabled(getenv func(string) string) bool {
	return getenv(PasswordResetOutboxEnableEnv) == "1" && ProducerOutboxEnabled(getenv)
}

func MagicLinkOutboxEnabled(getenv func(string) string) bool {
	return getenv(MagicLinkOutboxEnableEnv) == "1" && ProducerOutboxEnabled(getenv)
}

func MFARecoveryAlertOutboxEnabled(getenv func(string) string) bool {
	return getenv(MFARecoveryAlertOutboxEnableEnv) == "1" && ProducerOutboxEnabled(getenv)
}

func RecoveryEnabled(getenv func(string) string) bool {
	return Enabled(getenv) && getenv(RecoveryEnableEnv) == "1"
}

type Config struct {
	RecoveryID          string
	Socket              string
	Token               string
	Namespace           string
	IdentityPath        string
	KeyringPath         string
	SendingIP           netip.Addr
	SMTPAddr            string
	Delivery            bool
	Reports             bool
	ReportToken         string
	Feedback            bool
	FeedbackSources     map[string]FeedbackSource
	Retention           bool
	RetentionPolicy     RetentionPolicy
	Operations          bool
	OperationsToken     string
	AllowedRecipients   map[string]struct{}
	DailyRecipientLimit int
	Producers           map[string]Producer
	PacingPolicy        PacingPolicy
}

// LoadConfig returns before reading credentials or delivery configuration when
// disabled. The daemon must also return before opening a DB or listener.
func LoadConfig(getenv func(string) string) (Config, error) {
	if !Enabled(getenv) {
		return Config{}, nil
	}
	c := Config{Socket: getenv("FAAS_INTERNAL_MAIL_SOCKET"), Token: getenv("FAAS_INTERNAL_MAIL_TOKEN"), Namespace: getenv("FAAS_INTERNAL_MAIL_NAMESPACE"), IdentityPath: getenv("FAAS_INTERNAL_MAIL_AGE_IDENTITY_PATH"), SMTPAddr: getenv("FAAS_INTERNAL_MAIL_SMTP_ADDR"), Delivery: getenv(DeliveryEnableEnv) == "1"}
	if RecoveryEnabled(getenv) {
		c.RecoveryID = getenv(RecoveryIDEnv)
		if !canonicalOperatorID(c.RecoveryID) {
			return Config{}, errors.New("internal mail: recovery requires a fresh externally supplied canonical restore UUID")
		}
	}
	c.KeyringPath = getenv("FAAS_INTERNAL_MAIL_KEYRING_PATH")
	if c.KeyringPath != "" && (!filepath.IsAbs(c.KeyringPath) || filepath.Clean(c.KeyringPath) != c.KeyringPath) {
		return Config{}, errors.New("internal mail: keyring must be an absolute clean path")
	}
	c.Reports = ReportsEnabled(getenv)
	if BackpressureEnabled(getenv) && !c.Reports {
		return Config{}, errors.New("internal mail: relay backpressure requires the private reports collector and credential")
	}
	if c.Reports {
		c.ReportToken = getenv("FAAS_INTERNAL_MAIL_REPORT_TOKEN")
		if !validToken(c.ReportToken) || c.ReportToken == c.Token {
			return Config{}, errors.New("internal mail: reports require a separate service token of at least 32 bytes")
		}
	}
	c.Feedback = FeedbackEnabled(getenv)
	if ReputationEnabled(getenv) && !c.Feedback {
		return Config{}, errors.New("internal mail: reputation protection requires authenticated provider feedback")
	}
	if c.Feedback {
		var err error
		c.FeedbackSources, err = parseFeedbackSources(getenv("FAAS_INTERNAL_MAIL_FEEDBACK_SOURCES"), c.Token, c.ReportToken)
		if err != nil {
			return Config{}, err
		}
	}
	c.Operations = OperationsEnabled(getenv)
	if c.Operations {
		c.OperationsToken = getenv("FAAS_INTERNAL_MAIL_OPERATIONS_TOKEN")
		if err := validateOperationsToken(c.OperationsToken, c.Token, c.ReportToken, c.FeedbackSources); err != nil {
			return Config{}, err
		}
	}
	c.Retention = RetentionEnabled(getenv)
	if c.Retention {
		var err error
		c.RetentionPolicy, err = loadRetentionPolicy(getenv)
		if err != nil {
			return Config{}, err
		}
	}
	if EventFeedEnabled(getenv) && !ProducersEnabled(getenv) {
		return Config{}, errors.New("internal mail: event feeds require enrolled producers")
	}
	if ProducerLifecycleEnabled(getenv) && !ProducersEnabled(getenv) {
		return Config{}, errors.New("internal mail: producer lifecycle requires enrolled producers")
	}
	if ProducersEnabled(getenv) {
		var err error
		c.Producers, err = parseProducers(getenv("FAAS_INTERNAL_MAIL_PRODUCERS"), c.Token, c.ReportToken, c.OperationsToken, c.FeedbackSources)
		if err != nil {
			return Config{}, err
		}
	}
	if PacingEnabled(getenv) {
		var err error
		c.PacingPolicy, err = loadPacingPolicy(getenv)
		if err != nil {
			return Config{}, err
		}
	}
	if c.Socket == "" {
		c.Socket = DefaultSocket
	}
	if c.Namespace == "" {
		c.Namespace = "platform"
	}
	if !filepath.IsAbs(c.Socket) || filepath.Clean(c.Socket) != c.Socket {
		return Config{}, errors.New("internal mail: socket must be an absolute clean path")
	}
	if !validToken(c.Token) {
		return Config{}, errors.New("internal mail: a service token of at least 32 bytes is required")
	}
	if !validNamespace(c.Namespace) {
		return Config{}, errors.New("internal mail: invalid namespace")
	}
	if c.IdentityPath == "" && c.KeyringPath == "" {
		return Config{}, errors.New("internal mail: age identity path is required")
	}
	var err error
	c.SendingIP, err = netip.ParseAddr(getenv("FAAS_INTERNAL_MAIL_SENDING_IP"))
	if err != nil || !c.SendingIP.IsGlobalUnicast() || c.SendingIP.IsPrivate() {
		return Config{}, errors.New("internal mail: a public sending IP is required for DNS records")
	}
	if c.Delivery && !LoopbackSMTP(c.SMTPAddr) {
		return Config{}, errors.New("internal mail: delivery requires a loopback SMTP relay address")
	}
	c.AllowedRecipients, err = parseAllowedRecipients(getenv("FAAS_INTERNAL_MAIL_ALLOWED_RECIPIENTS"))
	if err != nil {
		return Config{}, err
	}
	c.DailyRecipientLimit = api.InternalMailPilotDailyRecipients
	if value := getenv("FAAS_INTERNAL_MAIL_DAILY_RECIPIENT_LIMIT"); value != "" {
		c.DailyRecipientLimit, err = strconv.Atoi(value)
		if err != nil || c.DailyRecipientLimit < 1 || c.DailyRecipientLimit > api.InternalMailDailyRecipients {
			return Config{}, errors.New("internal mail: daily recipient limit must be between 1 and the internal maximum")
		}
	}
	if c.Delivery && len(c.AllowedRecipients) == 0 {
		return Config{}, errors.New("internal mail: delivery requires an explicit pilot recipient allowlist")
	}
	return c, nil
}

func parseAllowedRecipients(value string) (map[string]struct{}, error) {
	allowed := map[string]struct{}{}
	if value == "" {
		return allowed, nil
	}
	entries := strings.Split(value, ",")
	if len(entries) > api.InternalMailMaxPilotRecipients {
		return nil, errors.New("internal mail: too many pilot recipient entries")
	}
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		address, err := parseAddress(entry)
		if err != nil || address.Name != "" || !strings.EqualFold(address.Address, entry) || strings.ContainsAny(entry, "*?") {
			return nil, errors.New("internal mail: pilot recipients must be exact mailbox addresses")
		}
		allowed[address.Address] = struct{}{}
	}
	return allowed, nil
}

func (c Config) RecipientAllowed(address string) bool {
	_, ok := c.AllowedRecipients[address]
	return ok
}

func validToken(token string) bool {
	return len(token) >= api.InternalMailTokenMinBytes && !strings.ContainsAny(token, " \r\n\t")
}

func LoopbackSMTP(address string) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return false
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 {
		return false
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

func validNamespace(v string) bool {
	if len(v) == 0 || len(v) > 64 || v[0] < 'a' || v[0] > 'z' {
		return false
	}
	for _, c := range v {
		if !lowerAlphaNumeric(c) && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func lowerAlphaNumeric(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
}

func MFADisableOutboxEnabled(getenv func(string) string) bool {
	return getenv(MFADisableOutboxEnableEnv) == "1" && ProducerOutboxEnabled(getenv)
}

func MFADisabledAlertOutboxEnabled(getenv func(string) string) bool {
	return getenv(MFADisabledAlertOutboxEnableEnv) == "1" && ProducerOutboxEnabled(getenv)
}

func PasswordChangedAlertOutboxEnabled(getenv func(string) string) bool {
	return getenv(PasswordChangedAlertOutboxEnableEnv) == "1" && ProducerOutboxEnabled(getenv)
}

func MFAEnabledAlertOutboxEnabled(getenv func(string) string) bool {
	return getenv(MFAEnabledAlertOutboxEnableEnv) == "1" && ProducerOutboxEnabled(getenv)
}

func PaymentFailedOutboxEnabled(getenv func(string) string) bool {
	return getenv(PaymentFailedOutboxEnableEnv) == "1" && ProducerOutboxEnabled(getenv)
}

func AccountRestoredOutboxEnabled(getenv func(string) string) bool {
	return getenv(AccountRestoredOutboxEnableEnv) == "1" && ProducerOutboxEnabled(getenv)
}

func AccountSuspendedOutboxEnabled(getenv func(string) string) bool {
	return getenv(AccountSuspendedOutboxEnableEnv) == "1" && ProducerOutboxEnabled(getenv)
}

func ManagedPostgresCapOutboxEnabled(getenv func(string) string) bool {
	return getenv(ManagedPostgresCapOutboxEnableEnv) == "1" && ProducerOutboxEnabled(getenv)
}

func NonpaymentDeletionOutboxEnabled(getenv func(string) string) bool {
	return getenv(NonpaymentDeletionOutboxEnableEnv) == "1" && ProducerOutboxEnabled(getenv)
}

func QuotaWarningOutboxEnabled(getenv func(string) string) bool {
	return getenv(QuotaWarningOutboxEnableEnv) == "1" && ProducerOutboxEnabled(getenv)
}

func SubscriptionEndedOutboxEnabled(getenv func(string) string) bool {
	return getenv(SubscriptionEndedOutboxEnableEnv) == "1" && ProducerOutboxEnabled(getenv)
}

func DeploymentFailedOutboxEnabled(getenv func(string) string) bool {
	return getenv(DeploymentFailedOutboxEnableEnv) == "1" && ProducerOutboxEnabled(getenv)
}
