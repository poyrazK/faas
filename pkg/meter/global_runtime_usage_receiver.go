package meter

// adr: 802
import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

// Only meterd owns import. Its deployment must expose this handler through
// authenticated operator ingress; there is no customer unsigned billing API.
// This layer neither seals minutes nor enables regional execution.
type GlobalRuntimeUsageReceiver struct {
	Store              state.GlobalRuntimeUsageStore
	Cells, Directories runtimequota.Trust
	Signer             runtimequota.Signer
	Now                func() time.Time
}

func (r GlobalRuntimeUsageReceiver) Receive(ctx context.Context, e runtimequota.Envelope) (runtimequota.Envelope, error) {
	if r.Store == nil || r.Directories.CheckSigner(r.Signer, runtimequota.Directory) != nil {
		return runtimequota.Envelope{}, runtimequota.ErrTrust
	}
	u, err := r.Cells.VerifyRuntimeUsage(e)
	if err != nil {
		return runtimequota.Envelope{}, err
	}
	if u.Grant.IssuerID != r.Signer.IssuerID() {
		return runtimequota.Envelope{}, runtimequota.ErrBinding
	}
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	receipt, err := state.ImportGlobalRuntimeUsage(ctx, r.Store, r.Cells, e, now)
	if err != nil {
		return runtimequota.Envelope{}, err
	}
	return r.Signer.SignUsageImportAck(runtimequota.UsageImportAck{Usage: receipt.Usage, ImportedAtMicros: receipt.ImportedAt.UnixMicro()})
}

func (r GlobalRuntimeUsageReceiver) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, request.Body, api.GlobalRuntimeUsageEnvelopeMaxBytes))
	if err != nil {
		http.Error(w, "invalid usage envelope", http.StatusRequestEntityTooLarge)
		return
	}
	envelope, err := runtimequota.DecodeRuntimeUsage(raw)
	if err != nil {
		http.Error(w, "invalid usage envelope", http.StatusBadRequest)
		return
	}
	ack, err := r.Receive(request.Context(), envelope)
	if err != nil {
		http.Error(w, "usage import failed", runtimeUsageImportStatus(err))
		return
	}
	body, err := json.Marshal(ack)
	if err != nil || len(body) > api.GlobalRuntimeUsageAckEnvelopeMaxBytes {
		http.Error(w, "usage receipt unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func runtimeUsageImportStatus(err error) int {
	switch {
	case errors.Is(err, runtimequota.ErrTrust):
		return http.StatusForbidden
	case errors.Is(err, runtimequota.ErrInvalid), errors.Is(err, state.ErrInvalidArgument):
		return http.StatusBadRequest
	case errors.Is(err, runtimequota.ErrBinding), errors.Is(err, state.ErrConflict), errors.Is(err, state.ErrNotFound):
		return http.StatusConflict
	default:
		return http.StatusServiceUnavailable
	}
}
