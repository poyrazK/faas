package globaldirectory

// adr: 644
import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

// Deliver retries the original allocation against its retained HTTPS origin.
// An uncertain response keeps the issued charge; receipt storage never drains it.
func (q RuntimeQuotaAuthority) Deliver(ctx context.Context, verifier Verifier, owner, slug, id, region, key string) (runtimequota.DeliveryReceipt, error) {
	return q.deliver(ctx, verifier, owner, slug, id, region, key, "")
}

// DeliverWithConsent carries public, purpose-specific regional consent instead
// of a customer credential. The regional store checks consent under its journal
// lock, including on retries. Retirement remains independent of consent.
func (q RuntimeQuotaAuthority) DeliverWithConsent(ctx context.Context, verifier Verifier, owner, slug, id, region, consentID string) (runtimequota.DeliveryReceipt, error) {
	if !ValidID(consentID) {
		return runtimequota.DeliveryReceipt{}, runtimequota.ErrInvalid
	}
	return q.deliver(ctx, verifier, owner, slug, id, region, "", consentID)
}

func (q RuntimeQuotaAuthority) deliver(ctx context.Context, verifier Verifier, owner, slug, id, region, key, consentID string) (runtimequota.DeliveryReceipt, error) {
	if q.Store == nil || !q.Signer.Available() || q.Directories.CheckSigner(q.Signer, runtimequota.Directory) != nil {
		return runtimequota.DeliveryReceipt{}, runtimequota.ErrTrust
	}
	a, err := q.Store.RuntimeQuota(ctx, owner, slug, id)
	if err != nil {
		return runtimequota.DeliveryReceipt{}, err
	}
	t, err := quotaTarget(a, region)
	if err != nil {
		return runtimequota.DeliveryReceipt{}, err
	}
	var envelope runtimequota.Envelope
	suffix := ""
	if a.RetiredAt != nil {
		if t.ReleasedAt != nil {
			return runtimequota.DeliveryReceipt{}, ErrRuntimeQuotaRetired
		}
		envelope, err = q.Retirement(ctx, owner, slug, id, region)
		suffix = "/retirement"
	} else {
		if consentID == "" && !api.ValidAPIKeyFormat(key) {
			return runtimequota.DeliveryReceipt{}, ErrVerification
		}
		envelope, err = q.Issue(ctx, owner, slug, id, region)
		if consentID != "" {
			suffix = "/delegated"
		}
	}
	if err != nil {
		return runtimequota.DeliveryReceipt{}, err
	}
	b := runtimequota.Binding{CellID: t.CellID, Region: t.Region, CellGeneration: t.CellGeneration, RegionalAccountID: t.RegionalAccountID, RegionalOrgID: t.RegionalOrgID, RegionalAppID: t.RegionalAppID, RegionalAppSlug: t.RegionalAppSlug, APIBase: t.APIBase}
	d := runtimequota.Delivery{Binding: b, Authority: envelope}
	if a.RetiredAt == nil {
		d.ConsentID = consentID
	}
	body, err := json.Marshal(d)
	if err != nil {
		return runtimequota.DeliveryReceipt{}, runtimequota.ErrInvalid
	}
	receipt, err := deliverQuotaHTTP(ctx, verifier, b, id, suffix, key, body)
	if err != nil {
		return runtimequota.DeliveryReceipt{}, err
	}
	return q.checkDeliveryReceipt(envelope, b, a.RetiredAt != nil, receipt)
}

func deliverQuotaHTTP(ctx context.Context, verifier Verifier, b runtimequota.Binding, id, suffix, key string, body []byte) (runtimequota.DeliveryReceipt, error) {
	ctx, cancel := context.WithTimeout(ctx, api.GlobalImageReleaseRegionalTimeout)
	defer cancel()
	path := b.APIBase + "/v1/cells/" + b.CellID + "/apps/" + b.RegionalAppID + "/runtime-quotas/" + id + suffix
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, path, bytes.NewReader(body))
	if err != nil {
		return runtimequota.DeliveryReceipt{}, ErrVerification
	}
	req.Header.Set("Content-Type", "application/json")
	if suffix == "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := verifier.probeClient().Do(req)
	if err != nil {
		return runtimequota.DeliveryReceipt{}, ErrVerification
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return runtimequota.DeliveryReceipt{}, ErrVerification
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, api.RegionalRuntimeQuotaDeliveryMaxBytes+1))
	if err != nil {
		return runtimequota.DeliveryReceipt{}, ErrVerification
	}
	r, err := runtimequota.DecodeDeliveryReceipt(raw)
	if err != nil {
		return runtimequota.DeliveryReceipt{}, ErrVerification
	}
	return r, nil
}

func (q RuntimeQuotaAuthority) checkDeliveryReceipt(sent runtimequota.Envelope, binding runtimequota.Binding, retired bool, r runtimequota.DeliveryReceipt) (runtimequota.DeliveryReceipt, error) {
	var grant runtimequota.Grant
	var retirement runtimequota.Retirement
	var err error
	if retired {
		retirement, err = q.Directories.VerifyRetirement(sent, binding)
		grant = retirement.Grant
	} else {
		grant, err = q.Directories.VerifyGrant(sent, binding)
	}
	if err != nil {
		return runtimequota.DeliveryReceipt{}, ErrVerification
	}
	if r.Grant != nil {
		got, err := q.Directories.VerifyGrant(*r.Grant, binding)
		if err != nil || got != grant {
			return runtimequota.DeliveryReceipt{}, ErrVerification
		}
	} else if !retired && r.Retirement == nil {
		return runtimequota.DeliveryReceipt{}, ErrVerification
	}
	if r.Retirement != nil {
		got, err := q.Directories.VerifyRetirement(*r.Retirement, binding)
		if err != nil || got.Grant != grant || retired && got != retirement {
			return runtimequota.DeliveryReceipt{}, ErrVerification
		}
		if !retired {
			return runtimequota.DeliveryReceipt{}, ErrRuntimeQuotaRetired
		}
	} else if retired {
		return runtimequota.DeliveryReceipt{}, ErrVerification
	}
	return r, nil
}
