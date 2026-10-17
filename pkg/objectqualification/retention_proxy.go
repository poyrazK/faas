package objectqualification

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectrecovery"
)

type settlementObservation struct {
	id    string
	drop  atomic.Bool
	mu    sync.Mutex
	ack   objectrecovery.Acknowledgement
	count int
}

func (o *settlementObservation) snapshot() (objectrecovery.Acknowledgement, int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.ack, o.count
}

// Observe only the chosen disposable probe's reply. Preserve bytes for the
// adapter's own validation; losing this reply must leave the central job ready.
func (p *faultProxy) observeSettlement(response *http.Response) error {
	o := p.settlementProbe.Load()
	if o == nil || response.Request.Method != http.MethodPost || response.Request.URL.Query().Get(objectrecovery.Query) != "settle" || response.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, api.ObjectNodeRecoveryResponseMaxBytes+1))
	closeErr := response.Body.Close()
	response.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	var ack objectrecovery.Acknowledgement
	if len(body) > api.ObjectNodeRecoveryResponseMaxBytes || json.Unmarshal(body, &ack) != nil || ack.Version != 1 || ack.SettledAt.IsZero() {
		return errors.New("qualification received invalid node settlement acknowledgement")
	}
	if ack.ID != o.id {
		return nil
	}
	o.mu.Lock()
	o.ack, o.count = ack, o.count+1
	o.mu.Unlock()
	if o.drop.CompareAndSwap(true, false) {
		return errDroppedResponse
	}
	return nil
}
