package globaldirectory

// adr: 775
import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

type QuotaDeliveryWorker struct {
	Authority RuntimeQuotaAuthority
	Store     QuotaDeliveryStore
	Verifier  Verifier
	OnError   func()
}

func (w QuotaDeliveryWorker) Run(ctx context.Context) error {
	if w.Store == nil || !w.Authority.Signer.Available() || w.Authority.Directories.CheckSigner(w.Authority.Signer, runtimequota.Directory) != nil {
		return runtimequota.ErrTrust
	}
	ticker := time.NewTicker(api.GlobalQuotaDeliveryInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := w.Once(ctx); err != nil && ctx.Err() == nil && w.OnError != nil {
			w.OnError()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (w QuotaDeliveryWorker) Once(ctx context.Context) error {
	if w.Store == nil {
		return ErrConflict
	}
	jobs, err := w.Store.ClaimQuotaDeliveries(ctx)
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	failures := make(chan error, len(jobs))
	for _, j := range jobs {
		wg.Add(1)
		go func() { defer wg.Done(); failures <- w.deliver(ctx, j) }()
	}
	wg.Wait()
	close(failures)
	var errs []error
	for err := range failures {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (w QuotaDeliveryWorker) deliver(ctx context.Context, j QuotaDeliveryJob) error {
	work, cancel := context.WithTimeout(ctx, api.GlobalQuotaDeliveryTimeout)
	defer cancel()
	err := w.Authority.verifyDeliveryJob(j)
	if err == nil {
		err = w.Store.CheckQuotaDeliveryDispatch(work, j)
	}
	var receipt runtimequota.DeliveryReceipt
	if err == nil {
		var body []byte
		body, err = json.Marshal(j.Delivery)
		if err == nil {
			suffix := "/delegated"
			if j.Purpose == QuotaDeliveryRetirement {
				suffix = "/retirement"
			}
			receipt, err = deliverQuotaHTTP(work, w.Verifier, j.Delivery.Binding, j.AllocationID, suffix, "", body)
		}
		if err == nil {
			_, err = w.Authority.checkDeliveryReceipt(j.Delivery.Authority, j.Delivery.Binding, j.Purpose == QuotaDeliveryRetirement, receipt)
		}
	}
	// Cancellation never acknowledges work. A fresh worker can reclaim the
	// expired lease and send the exact retained envelope again.
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, ErrQuotaDeliveryClaim) {
		return err
	}
	status, code := quotaDeliveryOutcome(err)
	var ack *runtimequota.DeliveryReceipt
	if err == nil {
		ack = &receipt
	}
	finishCtx, finishCancel := context.WithTimeout(ctx, api.GlobalQuotaDeliveryFinishTimeout)
	defer finishCancel()
	finish := w.Store.FinishQuotaDelivery(finishCtx, j, status, code, ack)
	return errors.Join(err, finish)
}

func quotaDeliveryOutcome(err error) (string, string) {
	if err == nil {
		return QuotaDeliveryAccepted, ""
	}
	switch {
	case errors.Is(err, ErrRuntimeConsentUnavailable):
		return QuotaDeliveryBlocked, "consent"
	case errors.Is(err, ErrOwnerInactive), errors.Is(err, ErrOwnerPlanChanged), errors.Is(err, ErrOwnerForbidden), errors.Is(err, ErrOrgTargetsUnavailable):
		return QuotaDeliveryBlocked, "owner"
	case errors.Is(err, ErrStaleCell), errors.Is(err, ErrNotFound), errors.Is(err, ErrRuntimeQuotaIdentity):
		return QuotaDeliveryBlocked, "binding"
	case errors.Is(err, ErrRuntimeQuotaRetired):
		return QuotaDeliveryBlocked, "retired"
	case errors.Is(err, runtimequota.ErrTrust), errors.Is(err, runtimequota.ErrBinding), errors.Is(err, runtimequota.ErrInvalid):
		return QuotaDeliveryBlocked, "trust"
	case errors.Is(err, ErrVerification):
		return QuotaDeliveryPending, "uncertain"
	default:
		return QuotaDeliveryPending, "backend"
	}
}
