package api

import "time"

// RealtimeLimits describes the managed realtime endpoint allowance for a plan.
// It is kept separate from Limits so adding the opt-in realtime surface does
// not change the long-lived Limits struct consumed by every daemon.
type RealtimeLimits struct {
	Plan                Plan
	EndpointsPerApp     int
	EndpointsPerAccount int
}

var realtimeLimits = map[Plan]RealtimeLimits{
	PlanFree:  {Plan: PlanFree},
	PlanHobby: {Plan: PlanHobby, EndpointsPerApp: 2, EndpointsPerAccount: 10},
	PlanPro:   {Plan: PlanPro, EndpointsPerApp: 10, EndpointsPerAccount: 50},
	PlanScale: {Plan: PlanScale, EndpointsPerApp: 25, EndpointsPerAccount: 250},
}

func RealtimeLimitsFor(p Plan) (RealtimeLimits, bool) {
	l, ok := realtimeLimits[p]
	return l, ok
}

const (
	RealtimeCallbackAuthTokenMaxBytes = 256
	RealtimeAuthTokenMaxBytes         = 256
	RealtimePathMaxBytes              = 256
	RealtimeCallbackURLMaxBytes       = 2048
	// RealtimeMessageMaxBytes is the maximum decoded payload accepted by the
	// public connection-management API. The daemon enforces its own limit too;
	// keeping the API bound explicit prevents oversized requests from reaching
	// an owner node.
	RealtimeMessageMaxBytes = 1 << 20
	RealtimeSecretMasked    = "***"
)

const (
	DefaultRealtimeConnectPath    = "/realtime/connect"
	DefaultRealtimeMessagePath    = "/realtime/message"
	DefaultRealtimeDisconnectPath = "/realtime/disconnect"
)

type CreateManagedRealtimeEndpointRequest struct {
	CallbackURL       string `json:"callback_url"`
	ConnectPath       string `json:"connect_path,omitempty"`
	MessagePath       string `json:"message_path,omitempty"`
	DisconnectPath    string `json:"disconnect_path,omitempty"`
	CallbackAuthToken string `json:"callback_auth_token"`
	AuthToken         string `json:"auth_token,omitempty"`
	Enabled           *bool  `json:"enabled,omitempty"`
}

type UpdateManagedRealtimeEndpointRequest struct {
	CallbackURL       *string `json:"callback_url,omitempty"`
	ConnectPath       *string `json:"connect_path,omitempty"`
	MessagePath       *string `json:"message_path,omitempty"`
	DisconnectPath    *string `json:"disconnect_path,omitempty"`
	CallbackAuthToken *string `json:"callback_auth_token,omitempty"`
	AuthToken         *string `json:"auth_token,omitempty"`
	Enabled           *bool   `json:"enabled,omitempty"`
}

// ManagedRealtimeMessageRequest is a binary-safe message sent to one live
// connection or published to an endpoint channel. DataBase64 is decoded
// before the request is forwarded to the realtime owner.
type ManagedRealtimeMessageRequest struct {
	DataBase64 string `json:"data_base64"`
	Binary     bool   `json:"binary,omitempty"`
}

// ManagedRealtimeCloseRequest optionally supplies the WebSocket close reason.
type ManagedRealtimeCloseRequest struct {
	Reason string `json:"reason,omitempty"`
}

// ManagedRealtimePublishResponse reports how many local owner queues accepted
// a published message. A cross-node owner may return a different aggregate
// after the leased registry is enabled.
type ManagedRealtimePublishResponse struct {
	Queued int `json:"queued"`
}

type ManagedRealtimeEndpointResponse struct {
	ID                      string `json:"id"`
	AppID                   string `json:"app_id"`
	AccountID               string `json:"account_id"`
	CallbackURL             string `json:"callback_url"`
	ConnectPath             string `json:"connect_path"`
	MessagePath             string `json:"message_path"`
	DisconnectPath          string `json:"disconnect_path"`
	CallbackAuthTokenMasked string `json:"callback_auth_token_masked"`
	AuthTokenMasked         string `json:"auth_token_masked"`
	Enabled                 bool   `json:"enabled"`
	CreatedAt               string `json:"created_at"`
	UpdatedAt               string `json:"updated_at"`
}

func ManagedRealtimeEndpointResponseFromRow(id, appID, accountID, callbackURL, connectPath, messagePath, disconnectPath string, enabled bool, createdAt, updatedAt time.Time) ManagedRealtimeEndpointResponse {
	return ManagedRealtimeEndpointResponse{
		ID:                      id,
		AppID:                   appID,
		AccountID:               accountID,
		CallbackURL:             callbackURL,
		ConnectPath:             connectPath,
		MessagePath:             messagePath,
		DisconnectPath:          disconnectPath,
		CallbackAuthTokenMasked: RealtimeSecretMasked,
		AuthTokenMasked:         RealtimeSecretMasked,
		Enabled:                 enabled,
		CreatedAt:               FormatAlertTime(createdAt),
		UpdatedAt:               FormatAlertTime(updatedAt),
	}
}
