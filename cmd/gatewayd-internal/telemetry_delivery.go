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

// Every collapsed row must have an explicit durable insert acknowledgement.
func acknowledgeTelemetryRows(stream telemetryResponseStream, rows []gateway.RequestTelemetryRow) error {
	accepted := map[uuid.UUID]bool{}
	outcomes := 0
	var failure error
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			failure = fmt.Errorf("receive request telemetry outcome: %w", err)
			break
		}
		if outcomes >= len(rows) {
			return errors.New("request telemetry receiver returned extra outcomes")
		}
		if response != nil && response.GetOutcome() == "inserted" {
			accepted[rows[outcomes].EventID] = true
		} else {
			failure = errors.New("request telemetry receiver rejected a row")
		}
		outcomes++
	}
	if failure == nil && outcomes == len(rows) {
		return nil
	}
	if failure == nil {
		failure = fmt.Errorf("request telemetry acknowledged %d of %d rows", outcomes, len(rows))
	}
	return &gateway.RequestTelemetryDeliveryError{Accepted: accepted, Cause: failure}
}
