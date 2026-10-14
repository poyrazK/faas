package state

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrRealtimePublisherCredentialDenied = errors.New("state: invalid publisher credential")
var ErrRealtimePublisherPermission = errors.New("state: publisher channel or event outside grant")
var ErrRealtimePublisherRate = errors.New("state: publisher rate limit reached")

type RealtimePublisherCredentialRequest struct {
	MaxMessageTTLSeconds int      `json:"max_message_ttl_seconds,omitempty"`
	Name                 string   `json:"name"`
	Channels             []string `json:"channels"`
	EventTypes           []string `json:"event_types"`
	PublishesPerMinute   int      `json:"publishes_per_minute,omitempty"`
	MaxPayloadBytes      int      `json:"max_payload_bytes,omitempty"`
	TTLSeconds           int      `json:"ttl_seconds,omitempty"`
}
type RealtimePublisherCredential struct {
	MaxMessageTTLSeconds int        `json:"max_message_ttl_seconds,omitempty"`
	ID                   string     `json:"id"`
	EndpointID           string     `json:"endpoint_id"`
	Name                 string     `json:"name"`
	Channels             []string   `json:"channels"`
	EventTypes           []string   `json:"event_types"`
	PublishesPerMinute   int        `json:"publishes_per_minute"`
	MaxPayloadBytes      int        `json:"max_payload_bytes"`
	Generation           string     `json:"generation"`
	IssuedAt             time.Time  `json:"issued_at"`
	ExpiresAt            time.Time  `json:"expires_at"`
	RevokedAt            *time.Time `json:"revoked_at"`
	AcceptedCount        string     `json:"accepted_count"`
	ReplayCount          string     `json:"replay_count"`
	LastUsedAt           *time.Time `json:"last_used_at"`
	LastChannel          *string    `json:"last_channel"`
	LastSequence         *string    `json:"last_sequence"`
}
type RealtimePublisherCredentialIssued struct {
	Credential string                      `json:"credential"`
	Info       RealtimePublisherCredential `json:"info"`
}
type RealtimePublisherMessageRequest struct {
	TTLSeconds int               `json:"ttl_seconds,omitempty"`
	EventType  string            `json:"event_type"`
	DataBase64 string            `json:"data_base64"`
	Binary     bool              `json:"binary,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}
type RealtimePublisherResult struct {
	Message  ManagedRealtimeChannelMessage
	Replayed bool
}
type RealtimePublisherCredentialStore interface {
	IssueRealtimePublisherCredential(context.Context, string, RealtimePublisherCredentialRequest) (RealtimePublisherCredentialIssued, error)
	ListRealtimePublisherCredentials(context.Context, string) ([]RealtimePublisherCredential, error)
	RotateRealtimePublisherCredential(context.Context, string, string, int) (RealtimePublisherCredentialIssued, error)
	RevokeRealtimePublisherCredential(context.Context, string, string) error
	AuthenticateRealtimePublisherCredential(context.Context, string, string) (RealtimePublisherCredential, error)
	PublishRealtimeWithCredential(context.Context, string, string, string, RealtimePublisherMessageRequest, string) (RealtimePublisherResult, error)
}

func realtimePublisherTokenHash(token string) ([]byte, error) {
	if len(token) != len("grp_")+base64.RawURLEncoding.EncodedLen(32) || !strings.HasPrefix(token, "grp_") {
		return nil, ErrRealtimePublisherCredentialDenied
	}
	raw, err := base64.RawURLEncoding.DecodeString(token[4:])
	if err != nil || len(raw) != 32 {
		return nil, ErrRealtimePublisherCredentialDenied
	}
	hash := sha256.Sum256([]byte(token))
	return hash[:], nil
}
func newRealtimePublisherToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token := "grp_" + base64.RawURLEncoding.EncodeToString(raw)
	hash, _ := realtimePublisherTokenHash(token)
	return token, hash, nil
}
func normalizeRealtimePublisherPolicy(req *RealtimePublisherCredentialRequest) error {
	if req.TTLSeconds == 0 {
		req.TTLSeconds = api.RealtimePublisherCredentialTTLDefaultSeconds
	}
	if req.PublishesPerMinute == 0 {
		req.PublishesPerMinute = api.RealtimePublisherPublishesDefaultPerMinute
	}
	if req.MaxPayloadBytes == 0 {
		req.MaxPayloadBytes = api.RealtimePublisherPayloadMaxBytes
	}
	if req.MaxMessageTTLSeconds < 0 || req.MaxMessageTTLSeconds > api.RealtimeMessageTTLMaxSeconds {
		return ErrInvalidArgument
	}
	if !validRealtimeWorkerName(req.Name, api.RealtimeWorkerCredentialNameMaxBytes) || req.TTLSeconds < 1 || req.TTLSeconds > api.RealtimePublisherCredentialTTLMaxSeconds || req.PublishesPerMinute < 1 || req.PublishesPerMinute > api.RealtimePublisherPublishesMaxPerMinute || req.MaxPayloadBytes < 1 || req.MaxPayloadBytes > api.RealtimePublisherPayloadMaxBytes || len(req.Channels) < 1 || len(req.Channels) > api.RealtimePublisherChannelsMax || len(req.EventTypes) < 1 || len(req.EventTypes) > api.RealtimePublisherEventTypesMax {
		return ErrInvalidArgument
	}
	seen := map[string]bool{}
	for _, ch := range req.Channels {
		if validateManagedRealtimeHistoryRequest("endpoint", ch) != nil || seen[ch] {
			return ErrInvalidArgument
		}
		seen[ch] = true
	}
	seen = map[string]bool{}
	for _, event := range req.EventTypes {
		if !validRealtimeWorkerName(event, api.RealtimePublisherEventTypeMaxBytes) || seen[event] {
			return ErrInvalidArgument
		}
		seen[event] = true
	}
	return nil
}
func realtimePublisherKeyValid(key string) bool {
	if len(key) < 1 || len(key) > api.RealtimePublisherIdempotencyMaxBytes {
		return false
	}
	for _, r := range key {
		if r < 33 || r > 126 {
			return false
		}
	}
	return true
}
