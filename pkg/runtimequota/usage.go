package runtimequota

// adr: 798
import (
	"encoding/hex"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// RuntimeUsage is a cell meter's immutable, complete compute observation for
// one original allocation and UTC minute. SourceDigest identifies the retained
// regional measurements. A signature authenticates the meter, not residency;
// producers must derive quantities from durable original resource intervals.
// Zero is explicit coverage. Missing observations never mean zero consumption.
type RuntimeUsage struct {
	Grant            Grant
	MinuteMicros     int64
	MBSeconds        int64
	SourceCount      int64
	SourceDigest     string
	ObservedAtMicros int64
}

func (u RuntimeUsage) Validate() error {
	minute := time.UnixMicro(u.MinuteMicros).UTC()
	issuedMinute := time.UnixMicro(u.Grant.IssuedAtMicros).UTC().Truncate(time.Minute)
	digest, err := hex.DecodeString(u.SourceDigest)
	if u.Grant.validateIdentity() != nil || u.MinuteMicros < 1 || u.MinuteMicros > api.GlobalRuntimeQuotaGenerationMax || !minute.Equal(minute.Truncate(time.Minute)) || minute.Before(issuedMinute) ||
		u.ObservedAtMicros < minute.Add(time.Minute).UnixMicro() || u.ObservedAtMicros > api.GlobalRuntimeQuotaGenerationMax ||
		u.MBSeconds < 0 || u.MBSeconds > api.GlobalRuntimeUsageQuantityMax || u.SourceCount < 0 || u.SourceCount > api.GlobalRuntimeUsageQuantityMax ||
		(u.MBSeconds == 0) != (u.SourceCount == 0) || err != nil || len(digest) != 32 || hex.EncodeToString(digest) != u.SourceDigest {
		return ErrInvalid
	}
	return nil
}

func DecodeRuntimeUsage(raw []byte) (Envelope, error) {
	return decodeBounded(raw, api.GlobalRuntimeUsageEnvelopeMaxBytes)
}

func (s Signer) SignRuntimeUsage(u RuntimeUsage) (Envelope, error) {
	if u.Validate() != nil || u.Grant.Binding.CellID != s.IssuerID() {
		return Envelope{}, ErrInvalid
	}
	return signBounded(s, "runtime-usage", u, api.GlobalRuntimeUsagePayloadMaxBytes)
}

func (t Trust) VerifyRuntimeUsage(e Envelope) (RuntimeUsage, error) {
	m, err := verifyBounded[RuntimeUsage](t, e, "runtime-usage", Cell, api.GlobalRuntimeUsagePayloadMaxBytes, api.GlobalRuntimeUsageEnvelopeMaxBytes)
	if err != nil {
		return RuntimeUsage{}, err
	}
	if m.Claims.Validate() != nil || m.Claims.Grant.Binding.CellID != m.IssuerID {
		return RuntimeUsage{}, ErrInvalid
	}
	return m.Claims, nil
}
