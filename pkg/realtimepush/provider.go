// Package realtimepush sends bounded notification wake-ups without copying inbox payloads.
package realtimepush

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/oci"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

type Config struct {
	Provider           string          `json:"provider"`
	ProjectID          string          `json:"project_id,omitempty"`
	ServiceAccountJSON json.RawMessage `json:"service_account_json,omitempty"`
	TeamID             string          `json:"team_id,omitempty"`
	KeyID              string          `json:"key_id,omitempty"`
	Topic              string          `json:"topic,omitempty"`
	PrivateKey         string          `json:"private_key,omitempty"`
	Sandbox            bool            `json:"sandbox,omitempty"`
	Subject            string          `json:"subject,omitempty"`
	Title              string          `json:"title,omitempty"`
	Body               string          `json:"body,omitempty"`
}

type Target struct {
	Token    string `json:"token,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	P256DH   string `json:"p256dh,omitempty"`
	Auth     string `json:"auth,omitempty"`
}

type Notification struct {
	Priority     string `json:"priority"`
	GroupKey     string `json:"group_key,omitempty"`
	MessageCount int    `json:"message_count"`
	Category     string `json:"category"`
	DeliveryID   string `json:"delivery_id"`
	EndpointID   string `json:"endpoint_id"`
	MessageID    string `json:"message_id"`
	Sequence     int64  `json:"sequence"`
}

type Result struct {
	StatusCode    int
	Retry         bool
	InvalidTarget bool
	Code          string
}

var validProject = regexp.MustCompile(`^[a-z][a-z0-9-]{4,62}[a-z0-9]$`)
var validAppleID = regexp.MustCompile(`^[A-Z0-9]{10}$`)

func ValidateConfig(c Config) error {
	if len(c.Title) > 256 || len(c.Body) > 1024 {
		return errors.New("notification text too long")
	}
	switch c.Provider {
	case "fcm":
		if !validProject.MatchString(c.ProjectID) {
			return errors.New("invalid FCM project")
		}
		jwt, err := google.JWTConfigFromJSON(c.ServiceAccountJSON, "https://www.googleapis.com/auth/firebase.messaging")
		if err != nil {
			return err
		}
		if !strings.Contains(jwt.Email, "@") || strings.ContainsAny(jwt.Email, "\r\n\x00 ") {
			return errors.New("invalid FCM service account")
		}
		block, _ := pem.Decode(jwt.PrivateKey)
		if block == nil {
			return errors.New("missing FCM private key")
		}
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			rsaKey, e := x509.ParsePKCS1PrivateKey(block.Bytes)
			if e != nil {
				return errors.New("invalid FCM private key")
			}
			key = rsaKey
		}
		if _, ok := key.(*rsa.PrivateKey); !ok {
			return errors.New("FCM requires an RSA private key")
		}
		return nil
	case "apns":
		if !validAppleID.MatchString(c.TeamID) || !validAppleID.MatchString(c.KeyID) || c.Topic == "" || len(c.Topic) > 256 || strings.ContainsAny(c.Topic, "\r\n\x00") {
			return errors.New("invalid APNs identifiers")
		}
		_, err := appleKey(c.PrivateKey)
		return err
	case "webpush":
		u, err := url.Parse(c.Subject)
		if err != nil || len(c.Subject) > 256 || strings.ContainsAny(c.Subject, "\r\n\x00") || !(u.Scheme == "mailto" && u.Opaque != "" || u.Scheme == "https" && u.Host != "") {
			return errors.New("invalid VAPID subject")
		}
		_, err = vapidKey(c.PrivateKey)
		return err
	default:
		return errors.New("unknown push provider")
	}
}

func ValidateTarget(provider string, t Target) error {
	switch provider {
	case "fcm":
		if t.Token == "" || len(t.Token) > 4096 || strings.ContainsAny(t.Token, "\x00\r\n ") {
			return errors.New("invalid FCM token")
		}
	case "apns":
		if len(t.Token) != 64 {
			return errors.New("invalid APNs token")
		}
		if _, err := hex.DecodeString(t.Token); err != nil {
			return err
		}
	case "webpush":
		u, err := url.Parse(t.Endpoint)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(t.Endpoint) > 2048 || (u.Port() != "" && u.Port() != "443") {
			return errors.New("invalid Web Push endpoint")
		}
		if _, _, err = webKeys(t); err != nil {
			return err
		}
	default:
		return errors.New("unknown push provider")
	}
	return nil
}

func NewClient() *http.Client {
	client := oci.NewEgressHTTPClient()
	if tr, ok := client.Transport.(*http.Transport); ok {
		tr = tr.Clone()
		tr.ForceAttemptHTTP2 = true
		client.Transport = tr
	}
	client.Timeout = 15 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client
}

func Send(ctx context.Context, client *http.Client, c Config, t Target, n Notification) Result {
	if n.MessageCount < 1 {
		n.MessageCount = 1
	}
	if ValidateConfig(c) != nil {
		return Result{Retry: true, Code: "configuration_invalid"}
	}
	if ValidateTarget(c.Provider, t) != nil {
		return Result{InvalidTarget: true, Code: "target_invalid"}
	}
	if c.Title == "" {
		c.Title = "New notification"
	}
	if c.Body == "" {
		c.Body = "Open the app to view it."
	}
	var request *http.Request
	var err error
	switch c.Provider {
	case "fcm":
		jwt, _ := google.JWTConfigFromJSON(c.ServiceAccountJSON, "https://www.googleapis.com/auth/firebase.messaging")
		// Never honor a customer-controlled OAuth endpoint from service-account JSON.
		jwt.TokenURL = "https://oauth2.googleapis.com/token"
		token, tokenErr := jwt.TokenSource(context.WithValue(ctx, oauth2.HTTPClient, client)).Token()
		if tokenErr != nil {
			return Result{Retry: true, Code: "provider_auth_unavailable"}
		}
		body, _ := json.Marshal(map[string]any{"message": map[string]any{"token": t.Token, "notification": map[string]string{"title": c.Title, "body": c.Body}, "android": map[string]any{"notification": map[string]string{"tag": n.DeliveryID}}, "data": map[string]string{"priority": n.Priority, "group_key": n.GroupKey, "message_count": strconv.Itoa(n.MessageCount), "category": n.Category, "delivery_id": n.DeliveryID, "endpoint_id": n.EndpointID, "message_id": n.MessageID, "sequence": strconv.FormatInt(n.Sequence, 10)}}})
		request, err = http.NewRequestWithContext(ctx, "POST", "https://fcm.googleapis.com/v1/projects/"+c.ProjectID+"/messages:send", bytes.NewReader(body))
		if err == nil {
			request.Header.Set("Authorization", "Bearer "+token.AccessToken)
			request.Header.Set("Content-Type", "application/json")
		}
	case "apns":
		key, _ := appleKey(c.PrivateKey)
		auth, signErr := signJWT(key, map[string]string{"alg": "ES256", "kid": c.KeyID}, map[string]any{"iss": c.TeamID, "iat": time.Now().Unix()})
		if signErr != nil {
			return Result{Retry: true, Code: "provider_auth_unavailable"}
		}
		host := "https://api.push.apple.com"
		if c.Sandbox {
			host = "https://api.sandbox.push.apple.com"
		}
		body, _ := json.Marshal(map[string]any{"aps": map[string]any{"alert": map[string]string{"title": c.Title, "body": c.Body}, "sound": "default", "thread-id": notificationThread(n)}, "priority": n.Priority, "group_key": n.GroupKey, "message_count": n.MessageCount, "category": n.Category, "delivery_id": n.DeliveryID, "endpoint_id": n.EndpointID, "message_id": n.MessageID, "sequence": n.Sequence})
		request, err = http.NewRequestWithContext(ctx, "POST", host+"/3/device/"+strings.ToLower(t.Token), bytes.NewReader(body))
		if err == nil {
			request.Header.Set("authorization", "bearer "+auth)
			request.Header.Set("apns-topic", c.Topic)
			request.Header.Set("apns-push-type", "alert")
			request.Header.Set("apns-priority", "10")
			request.Header.Set("apns-id", n.DeliveryID)
			request.Header.Set("apns-expiration", strconv.FormatInt(time.Now().Add(24*time.Hour).Unix(), 10))
			request.Header.Set("content-type", "application/json")
		}
	case "webpush":
		request, err = webRequest(ctx, c, t, n)
	}
	if err != nil {
		return Result{Retry: true, Code: "request_unavailable"}
	}
	response, err := client.Do(request)
	if err != nil {
		return Result{Retry: true, Code: "provider_unreachable"}
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 32769))
	result := Result{StatusCode: response.StatusCode}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return result
	}
	result.Code = "provider_rejected"
	result.Retry = response.StatusCode >= 500 || response.StatusCode == 408 || response.StatusCode == 429 || response.StatusCode == 401 || response.StatusCode == 403
	if (c.Provider == "webpush" && (response.StatusCode == 404 || response.StatusCode == 410)) || (c.Provider == "apns" && response.StatusCode == 410) {
		return Result{StatusCode: response.StatusCode, InvalidTarget: true, Code: "target_unregistered"}
	}
	if readErr != nil || len(body) > 32768 {
		return result
	}
	switch c.Provider {
	case "webpush":
		result.InvalidTarget = response.StatusCode == 404 || response.StatusCode == 410
	case "apns":
		var parsed struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(body, &parsed)
		result.InvalidTarget = response.StatusCode == 410 || parsed.Reason == "BadDeviceToken" || parsed.Reason == "DeviceTokenNotForTopic"
	case "fcm":
		var parsed struct {
			Error struct {
				Details []struct {
					Code string `json:"errorCode"`
				} `json:"details"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &parsed)
		for _, detail := range parsed.Error.Details {
			if detail.Code == "UNREGISTERED" {
				result.InvalidTarget = true
			}
		}
	}
	if result.InvalidTarget {
		result.Code = "target_unregistered"
		result.Retry = false
	}
	return result
}

func appleKey(raw string) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, errors.New("missing APNs private key")
	}
	value, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := value.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, errors.New("APNs requires a P-256 key")
	}
	return key, nil
}
func vapidKey(raw string) (*ecdsa.PrivateKey, error) {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(data) != 32 {
		return nil, errors.New("invalid VAPID private key")
	}
	d := new(big.Int).SetBytes(data)
	if d.Sign() == 0 || d.Cmp(elliptic.P256().Params().N) >= 0 {
		return nil, errors.New("invalid VAPID scalar")
	}
	x, y := elliptic.P256().ScalarBaseMult(data)
	return &ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, D: d}, nil
}
func signJWT(key *ecdsa.PrivateKey, header map[string]string, claims map[string]any) (string, error) {
	h, _ := json.Marshal(header)
	c, _ := json.Marshal(claims)
	unsigned := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	hash := sha256.Sum256([]byte(unsigned))
	r, s, err := ecdsa.Sign(rand.Reader, key, hash[:])
	if err != nil {
		return "", err
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func notificationThread(n Notification) string {
	if n.GroupKey == "" {
		return n.Category
	}
	sum := sha256.Sum256([]byte(n.EndpointID + "\x00" + n.Category + "\x00" + n.GroupKey))
	return hex.EncodeToString(sum[:])
}
