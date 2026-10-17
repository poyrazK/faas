package api

import "time"

type IssueBreadcrumb struct {
	OccurredAt time.Time `json:"occurred_at"`
	Category   string    `json:"category"`
	Level      string    `json:"level"`
	Message    string    `json:"message"`
}
