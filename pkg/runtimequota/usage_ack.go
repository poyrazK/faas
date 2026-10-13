package runtimequota

// adr: 802
import (
	"github.com/onebox-faas/faas/pkg/api"
)

// UsageImportAck attests committed canonical import only. It grants no
// runtime admission, native residency, absence, or physical drain authority.
// Claims, rather than an envelope signature, remain stable across key rotation.
// Import time is the original database clock; it is not ordered against the
// cell observation clock. The receiver signs only after the import commits.
type UsageImportAck struct {
	Usage            RuntimeUsage
	ImportedAtMicros int64
}

func (a UsageImportAck) Validate() error {
	if a.Usage.Validate() != nil || a.ImportedAtMicros < 1 || a.ImportedAtMicros > api.GlobalRuntimeQuotaGenerationMax {
		return ErrInvalid
	}
	return nil
}

func DecodeUsageImportAck(raw []byte) (Envelope, error) {
	return decodeBounded(raw, api.GlobalRuntimeUsageAckEnvelopeMaxBytes)
}

func (s Signer) SignUsageImportAck(a UsageImportAck) (Envelope, error) {
	if a.Validate() != nil || a.Usage.Grant.IssuerID != s.IssuerID() {
		return Envelope{}, ErrInvalid
	}
	return signBounded(s, "runtime-usage-import", a, api.GlobalRuntimeUsageAckPayloadMaxBytes)
}

func (t Trust) VerifyUsageImportAck(e Envelope) (UsageImportAck, error) {
	m, err := verifyBounded[UsageImportAck](t, e, "runtime-usage-import", Directory, api.GlobalRuntimeUsageAckPayloadMaxBytes, api.GlobalRuntimeUsageAckEnvelopeMaxBytes)
	if err != nil {
		return UsageImportAck{}, err
	}
	if m.Claims.Validate() != nil || m.Claims.Usage.Grant.IssuerID != m.IssuerID {
		return UsageImportAck{}, ErrInvalid
	}
	return m.Claims, nil
}
