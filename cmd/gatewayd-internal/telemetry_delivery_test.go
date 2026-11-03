package main

import (
	"errors"
	"io"
	"testing"

	"github.com/google/uuid"
	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/gateway"
)

type telemetryOutcomes struct {
	values []string
	err    error
}

func (s *telemetryOutcomes) Recv() (*apidpb.IncrementRequestTelemetryResponse, error) {
	if len(s.values) == 0 {
		if s.err != nil {
			return nil, s.err
		}
		return nil, io.EOF
	}
	value := s.values[0]
	s.values = s.values[1:]
	if value == "nil" {
		return nil, nil
	}
	return &apidpb.IncrementRequestTelemetryResponse{Outcome: value}, nil
}

func TestAcknowledgeTelemetryRows(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []string
		err    error
		pass   bool
		stream bool // the client connection must be discarded
	}{
		{"inserted", []string{"inserted", "inserted"}, nil, true, false},
		{"not_retained", []string{"inserted", "not_retained"}, nil, true, false},
		{"rate_limited", []string{"inserted", "rate_limited"}, nil, false, false},
		{"db_error", []string{"db_error"}, nil, false, true},
		{"partial", []string{"inserted"}, nil, false, true},
		{"unknown", []string{"ignored", "inserted"}, nil, false, false},
		{"nil", []string{"nil", "inserted"}, nil, false, false},
		{"extra", []string{"inserted", "inserted", "inserted"}, nil, false, true},
		{"transport_error", []string{"inserted"}, errors.New("disconnected"), false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := acknowledgeTelemetryRows(&telemetryOutcomes{values: tc.values, err: tc.err}, []gateway.RequestTelemetryRow{{EventID: uuid.New()}, {EventID: uuid.New()}})
			if (err == nil) != tc.pass {
				t.Fatalf("delivery outcome: %v", err)
			}
			if got := errors.Is(err, errTelemetryStream); got != tc.stream {
				t.Fatalf("stream failure = %v, want %v (%v)", got, tc.stream, err)
			}
		})
	}
}

func TestAcknowledgeTelemetryAppIsolation(t *testing.T) {
	rows := []gateway.RequestTelemetryRow{{EventID: uuid.New(), AppID: uuid.New()}, {EventID: uuid.New(), AppID: uuid.New()}, {EventID: uuid.New(), AppID: uuid.New()}}
	err := acknowledgeTelemetryRows(&telemetryOutcomes{values: []string{"rate_limited", "inserted", "db_error"}}, rows)
	var partial *gateway.RequestTelemetryDeliveryError
	if !errors.As(err, &partial) {
		t.Fatalf("want a partial delivery, got %v", err)
	}
	if !partial.Rejected[rows[0].EventID] || partial.Accepted[rows[0].EventID] {
		t.Fatalf("rate-limited row must be rejected for good: %+v", partial)
	}
	if !partial.Accepted[rows[1].EventID] {
		t.Fatalf("lost later app acknowledgement: %+v", partial)
	}
	if partial.Accepted[rows[2].EventID] || partial.Rejected[rows[2].EventID] {
		t.Fatalf("db_error row must stay retryable: %+v", partial)
	}
}
