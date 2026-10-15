package api

import "time"

// RealtimeUsageResponse is an account's realtime usage against its fair-use
// limits (ADR-1323). Realtime is free; these numbers are for visibility.
type RealtimeUsageResponse struct {
	Plan Plan `json:"plan"`
	// ConnectionLimit is the plan's concurrent connections per account; zero
	// when the plan does not include realtime.
	ConnectionLimit int `json:"connection_limit"`
	// OpenConnections counts connections on realtime nodes that reported in
	// the last 30 seconds.
	OpenConnections int64                       `json:"open_connections"`
	From            time.Time                   `json:"from"`
	To              time.Time                   `json:"to"`
	Hours           []RealtimeUsageHour         `json:"hours"`
	Endpoints       []RealtimeEndpointUsageHour `json:"endpoints"`
}

// RealtimeUsageHour is the account's usage in one UTC hour. PeakConnections is
// the most connections open at once in any minute of the hour.
type RealtimeUsageHour struct {
	Hour              time.Time `json:"hour"`
	PeakConnections   int64     `json:"peak_connections"`
	ConnectionSeconds int64     `json:"connection_seconds"`
	DeliveredMessages int64     `json:"delivered_messages"`
	DeliveredBytes    int64     `json:"delivered_bytes"`
	ReceivedMessages  int64     `json:"received_messages"`
	ReceivedBytes     int64     `json:"received_bytes"`
}

// RealtimeEndpointUsageHour is one endpoint's usage in one UTC hour.
type RealtimeEndpointUsageHour struct {
	EndpointID string `json:"endpoint_id"`
	AppID      string `json:"app_id"`
	RealtimeUsageHour
}
