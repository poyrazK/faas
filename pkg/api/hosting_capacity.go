package api

import "time"

// Counts describe stored admission capacity, not edge serving health.
type HostingPreviewCapacity struct {
	SiteID       string    `json:"site_id"`
	Limit        int64     `json:"limit"`
	Previews     int64     `json:"previews"`
	Reservations int64     `json:"reservations"`
	Available    int64     `json:"available"`
	ObservedAt   time.Time `json:"observed_at"`
}
