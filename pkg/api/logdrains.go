package api

import "time"

// AppLogDrainAuthHeaderMaxBytes bounds the single sealed authentication
// header accepted for a customer log destination.
const AppLogDrainAuthHeaderMaxBytes = 4096

const AppLogDrainAuthHeaderMasked = "***"

var AllowedAppLogDrainKinds = []string{"http_json", "otlp"}

type CreateAppLogDrainRequest struct {
	Kind       string `json:"kind"`
	TargetURL  string `json:"target_url"`
	AuthHeader string `json:"auth_header,omitempty"`
	Enabled    *bool  `json:"enabled,omitempty"`
}

type UpdateAppLogDrainRequest struct {
	Kind       *string `json:"kind,omitempty"`
	TargetURL  *string `json:"target_url,omitempty"`
	AuthHeader *string `json:"auth_header,omitempty"`
	Enabled    *bool   `json:"enabled,omitempty"`
}

type AppLogDrainResponse struct {
	ID               string `json:"id"`
	AppID            string `json:"app_id"`
	AccountID        string `json:"account_id"`
	Kind             string `json:"kind"`
	TargetURL        string `json:"target_url"`
	AuthHeaderMasked string `json:"auth_header_masked"`
	Enabled          bool   `json:"enabled"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

type AppLogDrainRow struct {
	ID            string
	AppID         string
	AccountID     string
	Kind          string
	TargetURL     string
	HasAuthHeader bool
	Enabled       bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func AppLogDrainResponseFromRow(r AppLogDrainRow) AppLogDrainResponse {
	masked := ""
	if r.HasAuthHeader {
		masked = AppLogDrainAuthHeaderMasked
	}
	return AppLogDrainResponse{
		ID:               r.ID,
		AppID:            r.AppID,
		AccountID:        r.AccountID,
		Kind:             r.Kind,
		TargetURL:        r.TargetURL,
		AuthHeaderMasked: masked,
		Enabled:          r.Enabled,
		CreatedAt:        FormatAlertTime(r.CreatedAt),
		UpdatedAt:        FormatAlertTime(r.UpdatedAt),
	}
}
