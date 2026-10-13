package meter

// adr: 802
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

const RuntimeUsageImportPath = "/v1/internal/runtime-usage"

// Destinations are trusted operator configuration keyed by the original
// directory issuer, never URLs or credentials supplied in usage claims.
type RuntimeUsageHTTPSender struct {
	origins map[string]string
	client  *http.Client
}

func NewRuntimeUsageHTTPSender(origins map[string]string, client *http.Client) (*RuntimeUsageHTTPSender, error) {
	if len(origins) == 0 {
		return nil, runtimequota.ErrTrust
	}
	copied := make(map[string]string, len(origins))
	for issuer, origin := range origins {
		id, err := uuid.Parse(issuer)
		if err != nil || id == uuid.Nil || id.String() != issuer || !api.ValidGlobalDirectoryAPIBase(origin) {
			return nil, runtimequota.ErrInvalid
		}
		copied[issuer] = origin
	}
	if client == nil {
		client = &http.Client{}
	}
	clone := *client
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &RuntimeUsageHTTPSender{origins: copied, client: &clone}, nil
}

func (s *RuntimeUsageHTTPSender) Send(ctx context.Context, item state.RegionalRuntimeUsageOutboxItem) (runtimequota.Envelope, error) {
	if s == nil || s.client == nil || item.Usage.Validate() != nil {
		return runtimequota.Envelope{}, runtimequota.ErrTrust
	}
	origin, ok := s.origins[item.Usage.Grant.IssuerID]
	if !ok {
		return runtimequota.Envelope{}, runtimequota.ErrTrust
	}
	body, err := json.Marshal(item.Envelope)
	if err != nil || len(body) > api.GlobalRuntimeUsageEnvelopeMaxBytes {
		return runtimequota.Envelope{}, runtimequota.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, api.RegionalRuntimeUsageDeliveryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+RuntimeUsageImportPath, bytes.NewReader(body))
	if err != nil {
		return runtimequota.Envelope{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(req)
	if err != nil {
		return runtimequota.Envelope{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return runtimequota.Envelope{}, state.ErrConflict
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, api.GlobalRuntimeUsageAckEnvelopeMaxBytes+1))
	if err != nil {
		return runtimequota.Envelope{}, err
	}
	return runtimequota.DecodeUsageImportAck(raw)
}

type RuntimeUsageSender interface {
	Send(context.Context, state.RegionalRuntimeUsageOutboxItem) (runtimequota.Envelope, error)
}

type RuntimeUsageDelivery struct {
	Store       state.RegionalRuntimeUsageOutboxStore
	Sender      RuntimeUsageSender
	Directories runtimequota.Trust
	Now         func() time.Time
}

type RuntimeUsageDeliveryPage struct {
	NextID    int64
	Imported  int
	FailedIDs []int64
}

// Concurrent senders and lost replies replay the global canonical import.
// A failing earlier item advances this traversal without marking delivery;
// the caller resets the cursor after an empty page and retries retained work.
func (d RuntimeUsageDelivery) DeliverPage(ctx context.Context, cell string, after int64, limit int) (RuntimeUsageDeliveryPage, error) {
	result := RuntimeUsageDeliveryPage{NextID: after}
	if d.Store == nil || d.Sender == nil {
		return result, runtimequota.ErrTrust
	}
	rows, err := d.Store.ListPendingRegionalRuntimeUsage(ctx, cell, after, limit)
	if err != nil {
		return result, err
	}
	if len(rows) == 0 {
		result.NextID = 0
		return result, nil
	}
	var failures error
	for _, item := range rows {
		if err := ctx.Err(); err != nil {
			return result, errors.Join(failures, err)
		}
		if item.ID <= result.NextID || item.Ack != nil || cell != "" && item.Usage.Grant.Binding.CellID != cell {
			return result, errors.Join(failures, state.ErrConflict)
		}
		result.NextID = item.ID
		ack, err := d.Sender.Send(ctx, item)
		if err == nil {
			err = d.recordAck(ctx, item, ack)
		}
		if err != nil {
			result.FailedIDs = append(result.FailedIDs, item.ID)
			failures = errors.Join(failures, err)
		} else {
			result.Imported++
		}
	}
	return result, failures
}

func (d RuntimeUsageDelivery) recordAck(ctx context.Context, item state.RegionalRuntimeUsageOutboxItem, e runtimequota.Envelope) error {
	ack, err := d.Directories.VerifyUsageImportAck(e)
	if err != nil {
		return err
	}
	if ack.Usage != item.Usage {
		return state.ErrConflict
	}
	now := time.Now()
	if d.Now != nil {
		now = d.Now()
	}
	_, err = state.AcknowledgeRegionalRuntimeUsage(ctx, d.Store, d.Directories, e, now)
	return err
}
