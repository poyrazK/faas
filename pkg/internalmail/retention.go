package internalmail

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const RetentionPath = APIPath + "/retention"

var ErrRetentionDryRun = errors.New("internal mail retention requires dry run")

type RetentionPolicy struct {
	Days      int
	BatchSize int
	DryRun    bool
}

func DefaultRetentionPolicy() RetentionPolicy {
	return RetentionPolicy{Days: api.InternalMailRetentionDays, BatchSize: api.InternalMailRetentionBatchSize, DryRun: true}
}

func loadRetentionPolicy(getenv func(string) string) (RetentionPolicy, error) {
	policy := DefaultRetentionPolicy()
	for _, setting := range []struct {
		name string
		dest *int
	}{
		{"FAAS_INTERNAL_MAIL_RETENTION_DAYS", &policy.Days},
		{"FAAS_INTERNAL_MAIL_RETENTION_BATCH_SIZE", &policy.BatchSize},
	} {
		if value := getenv(setting.name); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return RetentionPolicy{}, ErrInvalid
			}
			*setting.dest = parsed
		}
	}
	switch getenv("FAAS_INTERNAL_MAIL_RETENTION_DRY_RUN") {
	case "", "1":
	case "0":
		policy.DryRun = false
	default:
		return RetentionPolicy{}, ErrInvalid
	}
	return policy, policy.validate()
}

func (p RetentionPolicy) validate() error {
	if p.Days < 1 || p.Days > api.InternalMailMaxRetentionDays || p.BatchSize < 1 || p.BatchSize > api.InternalMailMaxRetentionBatchSize {
		return ErrInvalid
	}
	return nil
}

// RetentionResult is an aggregate, bounded batch report, never an inventory of
// message IDs, addresses or payloads. A full batch may leave more eligible work.
type RetentionResult struct {
	DryRun           bool  `json:"dry_run"`
	Days             int   `json:"days"`
	BatchSize        int   `json:"batch_size"`
	EligibleMessages int   `json:"eligible_messages"`
	EligibleBytes    int64 `json:"eligible_bytes"`
	PrunedMessages   int   `json:"pruned_messages"`
	PrunedBytes      int64 `json:"pruned_bytes"`
	SkippedMessages  int   `json:"skipped_messages"`
	BatchFull        bool  `json:"batch_full"`
}

type retentionStore interface {
	previewRetention(context.Context, RetentionPolicy) (RetentionResult, error)
	applyRetention(context.Context, RetentionPolicy, func(Message) ([]byte, error), func() bool) (RetentionResult, error)
}

func (s *Service) retentionEnabled() bool {
	return s.enabled() && s.RetentionEnabled != nil && s.RetentionEnabled()
}

// RetainPayloads performs at most one bounded pass. Configuration owns the
// retention window and deletion permission; a caller can always request preview
// but cannot override a dry-run-only deployment. Disabled means no store/key IO.
func (s *Service) RetainPayloads(ctx context.Context, dryRun bool) (result RetentionResult, err error) {
	if !s.retentionEnabled() {
		return result, ErrDisabled
	}
	if err := s.RetentionPolicy.validate(); err != nil {
		return result, err
	}
	if !dryRun && s.RetentionPolicy.DryRun {
		return result, ErrRetentionDryRun
	}
	store, ok := s.Store.(retentionStore)
	if !ok || !dryRun && s.Codec == nil {
		return result, ErrInvalid
	}
	defer func() {
		if s.OnRetention != nil {
			s.OnRetention(result, err)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, api.InternalMailRetentionTimeout)
	defer cancel()
	if dryRun {
		return store.previewRetention(ctx, s.RetentionPolicy)
	}
	return store.applyRetention(ctx, s.RetentionPolicy, s.retentionFingerprint, s.retentionEnabled)
}

func (s *Service) retentionFingerprint(m Message) ([]byte, error) {
	if !s.retentionEnabled() {
		return nil, ErrDisabled
	}
	plain, err := s.Codec.Open("message:"+m.ID, m.SealedPayload)
	if err != nil {
		return nil, errors.New("internal mail: retention payload verification failed")
	}
	defer clear(plain)
	if len(m.PayloadFingerprint) != 0 && verifyFingerprint(s.Codec, "message:"+m.ID, plain, m.PayloadFingerprint) != nil {
		return nil, errors.New("internal mail: retention fingerprint mismatch")
	}
	fingerprint, err := s.Codec.Fingerprint("message:"+m.ID, plain)
	if err != nil {
		return nil, errors.New("internal mail: retention fingerprint failed")
	}
	if _, err := fingerprintKeyID(fingerprint); err != nil {
		return nil, errors.New("internal mail: retention fingerprint failed")
	}
	return fingerprint, nil
}

// RunRetention runs independently of SMTP dispatch, once at startup and then
// hourly. Every pass owns its deadline; drain waits for its transaction to end.
func (s *Service) RunRetention(ctx context.Context, onError func(error)) {
	ticker := time.NewTicker(api.InternalMailRetentionInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		_, err := s.RetainPayloads(ctx, s.RetentionPolicy.DryRun)
		if err != nil && !errors.Is(err, ErrDisabled) && ctx.Err() == nil && onError != nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
