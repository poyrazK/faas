package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/gateway"
)

type telemetryResponseStream interface {
	Recv() (*apidpb.IncrementRequestTelemetryResponse, error)
}

// errTelemetryStream marks a delivery whose stream broke or answered out of
// step with the rows sent. Only then is the client connection discarded; a
// row-level outcome leaves the stream healthy.
var errTelemetryStream = errors.New("request telemetry stream failed")

// Every collapsed row must have an explicit durable acknowledgement. apid
// answers each row with one outcome:
//   - inserted, or not_retained (the app's plan keeps no request telemetry;
//     its usage was recorded first): the row is done;
//   - rate_limited: the plan's ingest cap is spent and the receiver contract
//     says not to retry, so the row is rejected for good. Retrying it only
//     held the publisher in backoff for a bucket that refills over a minute,
//     delaying every other app's rows behind it;
//   - db_error, an unknown or missing outcome: retryable.
func acknowledgeTelemetryRows(stream telemetryResponseStream, rows []gateway.RequestTelemetryRow) error {
	accepted := map[uuid.UUID]bool{}
	rejected := map[uuid.UUID]bool{}
	outcomes := 0
	var failure error
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			failure = fmt.Errorf("%w: receive outcome: %w", errTelemetryStream, err)
			break
		}
		if outcomes >= len(rows) {
			return fmt.Errorf("%w: receiver returned extra outcomes", errTelemetryStream)
		}
		switch outcome := response.GetOutcome(); outcome {
		case "inserted", "not_retained":
			accepted[rows[outcomes].EventID] = true
		case "rate_limited":
			rejected[rows[outcomes].EventID] = true
		default:
			if failure == nil {
				failure = fmt.Errorf("request telemetry receiver answered %q for a row", outcome)
			}
		}
		outcomes++
	}
	if failure == nil && outcomes == len(rows) && len(rejected) == 0 {
		return nil
	}
	if outcomes != len(rows) && !errors.Is(failure, errTelemetryStream) {
		failure = fmt.Errorf("%w: acknowledged %d of %d rows", errTelemetryStream, outcomes, len(rows))
	}
	if failure == nil {
		failure = fmt.Errorf("request telemetry receiver rate limited %d rows", len(rejected))
	}
	return &gateway.RequestTelemetryDeliveryError{Accepted: accepted, Rejected: rejected, Cause: failure}
}
