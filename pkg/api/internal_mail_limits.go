package api

import "time"

// Internal mail safeguards are independent of customer plans. The service is
// an operator-only experiment; these are not customer email entitlements.
const (
	InternalMailProducerOutboxMaxPending       = 1000
	InternalMailProducerOutboxMaxAttempts      = 32
	InternalMailProducerOutboxTTL              = 24 * time.Hour
	InternalMailProducerOutboxLease            = time.Minute
	InternalMailProducerOutboxPoll             = 5 * time.Second
	InternalMailProducerOutboxPassTimeout      = 20 * time.Second
	InternalMailProducerOutboxRetryBase        = 30 * time.Second
	InternalMailProducerOutboxRetryMax         = 30 * time.Minute
	InternalMailProducerOutboxMaintenanceBatch = 100
	InternalMailOutboxOperatorMaxConnections   = 1
	InternalMailVerificationIdentityMaxBytes   = 4096
	InternalMailVerificationCipherMaxBytes     = 4096
	InternalMailVerificationOriginMaxBytes     = 2048
	InternalMailMFARecoveryAlertTTL            = 24 * time.Hour
	InternalMailMFARecoveryAlertMaxBytes       = 512

	InternalMailMaxRequestBytes            = 1 << 20
	InternalMailMaxDSNBytes                = 1 << 20
	InternalMailMaxDSNStatusBytes          = 64 << 10
	InternalMailMaxFeedbackBytes           = 1 << 20
	InternalMailMaxFeedbackConfigBytes     = 32 << 10
	InternalMailMaxFeedbackSources         = 8
	InternalMailMaxFeedbackDomains         = 16
	InternalMailMaxFeedbackSourceBytes     = 32
	InternalMailMaxFeedbackSignatures      = 5
	InternalMailMaxProducerConfigBytes     = 32 << 10
	InternalMailMaxProducers               = 16
	InternalMailMaxProducerIDBytes         = 32
	InternalMailMaxProducerSenders         = 16
	InternalMailMaxEncryptionKeys          = 8
	InternalMailMaxKeyringConfigBytes      = 32 << 10
	InternalMailEncryptionBatchSize        = 10
	InternalMailMaxEncryptionBatchSize     = 25
	InternalMailMaxSuppressionAddressBytes = 254
	InternalMailCredentialOverlap          = time.Hour
	InternalMailMaxCredentialOverlap       = 24 * time.Hour
	InternalMailEventFeedBatchSize         = 100
	InternalMailEventFeedPageSize          = 25
	InternalMailEventFeedMaxPageSize       = 100
	InternalMailEventFeedMaxConsumers      = 8
	InternalMailEventConsumerNameBytes     = 32
	InternalMailEventFeedPollInterval      = time.Second
	InternalMailEventFeedPublishTimeout    = 15 * time.Second
	InternalMailProducerDailyRecipients    = 10
	InternalMailProducerPending            = 100
	InternalMailProducerDispatch           = 10
	InternalMailMaxProducerDispatch        = 100
	InternalMailProducerDispatchWindow     = time.Minute
	InternalMailMaxQueueInventoryBytes     = 8 << 20
	InternalMailMaxMonitoringBytes         = 4 << 20
	InternalMailMonitoringTimeout          = 3 * time.Second
	InternalMailMaxPilotRecipients         = 25
	InternalMailPilotDailyRecipients       = 10
	InternalMailMaxBodyBytes               = 256 << 10
	InternalMailMaxPayloadBytes            = 2 << 20
	InternalMailMaxRecipients              = 10
	InternalMailMaxHeaders                 = 16
	InternalMailMaxHeaderBytes             = 1024
	InternalMailMaxSubjectBytes            = 500
	InternalMailMaxIdempotencyBytes        = 128
	InternalMailMaxDiagnosticBytes         = 512
	InternalMailDailyRecipients            = 100
	InternalMailMaxPending                 = 1000
	InternalMailMaxAttempts                = 8
	InternalMailTokenMinBytes              = 32
	InternalMailMaxEvents                  = 1000
	InternalMailOperatorPageSize           = 25
	InternalMailMaxOperatorPageSize        = 100
	InternalMailMaxOperatorCursorBytes     = 320
	InternalMailMaxOperatorResponseBytes   = 1 << 20
	InternalMailOperatorRollbackTimeout    = 5 * time.Second
	InternalMailPollInterval               = time.Second
	InternalMailLeaseDuration              = time.Minute
	InternalMailSMTPTimeout                = 20 * time.Second
	InternalMailWorkerPassTimeout          = 30 * time.Second
	InternalMailAPITimeout                 = 15 * time.Second
	InternalMailDNSVerifyTimeout           = 10 * time.Second
	InternalMailMaxQueueAge                = 24 * time.Hour
	InternalMailMaxScheduleDelay           = 24 * time.Hour
	InternalMailDomainPacingInterval       = 5 * time.Second
	InternalMailMinDomainPacingInterval    = time.Second
	InternalMailMaxDomainPacingInterval    = 5 * time.Minute
	InternalMailMaxRelayHealthBytes        = 4096
	InternalMailRelayMaxSampleAge          = 90 * time.Second
	InternalMailRelayClockSkew             = 5 * time.Second
	InternalMailRelayRecoveryWindow        = 2 * time.Minute
	InternalMailRelayQueueHighWater        = 100
	InternalMailRelayQueueMaxAge           = 10 * time.Minute
	InternalMailRelayMaxObservedMessages   = 1000000
	InternalMailRelayMaxObservedAge        = 365 * 24 * time.Hour
	InternalMailRelayProbeTimeout          = 3 * time.Second
	InternalMailRelayCollectionTimeout     = 10 * time.Second
	InternalMailReputationWindow           = 24 * time.Hour
	InternalMailReputationMinRecipients    = 20
	InternalMailReputationMinBounces       = 3
	InternalMailReputationMinComplaints    = 2
	InternalMailReputationRateScale        = 10000
	InternalMailReputationBounceRate       = 500 // basis points: 5%
	InternalMailReputationComplaintRate    = 10  // basis points: 0.1%
	InternalMailRetentionDays              = 7
	InternalMailMaxRetentionDays           = 365
	InternalMailRetentionBatchSize         = 25
	InternalMailMaxRetentionBatchSize      = 100
	InternalMailRetentionInterval          = time.Hour
	InternalMailRetentionTimeout           = 15 * time.Second
	InternalMailRetentionRollbackTimeout   = 5 * time.Second
	InternalMailMaxLiveDKIMKeys            = 4
	InternalMailDKIMPropagationWait        = time.Hour
	InternalMailDKIMOverlap                = 7 * 24 * time.Hour
	InternalMailDKIMStageWarningAge        = 24 * time.Hour
	InternalMailDomainCheckInterval        = time.Hour
	InternalMailDomainCheckMaxAge          = 24 * time.Hour
	InternalMailDomainFailureSeparation    = 5 * time.Minute
	InternalMailDomainCheckPoll            = time.Minute
	InternalMailDomainCheckBatchSize       = 10
	InternalMailDomainCheckPassTimeout     = 30 * time.Second
	InternalMailDrainWaitTimeout           = 30 * time.Second
	InternalMailMaxDrainWaitTimeout        = 5 * time.Minute
	InternalMailDrainPollInterval          = time.Second
	InternalMailRetryBase                  = time.Minute
	InternalMailRetryMax                   = time.Hour
)

// MFA disable links allow one confirmation day after the existing one-day
// cooldown. Delivery stops at that original cooldown; retries extend neither.
const (
	MFADisableTokenBytes         = 32
	MFADisableEmailCooldown      = 24 * time.Hour
	MFADisableEmailConfirmWindow = 24 * time.Hour
	InternalMailMFADisableTTL    = 24 * time.Hour
)

const (
	InternalMailMFAEnabledAlertTTL           = 24 * time.Hour
	InternalMailMFAEnabledAlertMaxBytes      = 512
	InternalMailPasswordChangedAlertTTL      = 24 * time.Hour
	InternalMailPasswordChangedAlertMaxBytes = 512
	InternalMailMFADisabledAlertTTL          = 24 * time.Hour
	InternalMailMFADisabledAlertMaxBytes     = 512
)
