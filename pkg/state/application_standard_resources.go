package state

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

type ApplicationStandardLogDestination struct {
	api.ApplicationStandardLogDestination
	// Never marshaled into a customer response, audit record or plan.
	AuthHeaderSealed []byte `json:"-"`
}

type ApplicationStandardLogDestinationCreate struct {
	OrgID, ActorID, Name, Kind, TargetURL string
	AuthHeaderSealed                      []byte
}

type ApplicationStandardPublisherCreate struct {
	OrgID, ActorID, Name string
	PublicKeyDER         []byte
}

type ApplicationStandardResourceStore interface {
	CreateApplicationStandardLogDestination(context.Context, ApplicationStandardLogDestinationCreate) (ApplicationStandardLogDestination, error)
	GetApplicationStandardLogDestination(context.Context, string, string) (ApplicationStandardLogDestination, error)
	ListApplicationStandardLogDestinations(context.Context, string, string, int) ([]ApplicationStandardLogDestination, error)
	CreateApplicationStandardPublisher(context.Context, ApplicationStandardPublisherCreate) (api.ApplicationStandardPublisher, error)
	GetApplicationStandardPublisher(context.Context, string, string) (api.ApplicationStandardPublisher, error)
	ListApplicationStandardPublishers(context.Context, string, string, int) ([]api.ApplicationStandardPublisher, error)
}

func validStandardResourceIdentity(orgID, actorID, name string) bool {
	return validStandardResourceRead(orgID, actorID) && utf8.ValidString(name) && strings.TrimSpace(name) == name && name != "" && len(name) <= api.ApplicationStandardMaxResourceNameBytes && !strings.ContainsAny(name, "\r\n\x00")
}

func validStandardResourceRead(orgID, resourceID string) bool {
	for _, id := range []string{orgID, resourceID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil {
			return false
		}
	}
	return true
}

func validStandardResourcePage(orgID, after string, limit int) bool {
	return validStandardResourceRead(orgID, orgID) && (after == "" || validStandardResourceRead(orgID, after)) && limit > 0 && limit <= api.ApplicationStandardMaxListPage+1
}

func prepareStandardLogDestination(in ApplicationStandardLogDestinationCreate) (ApplicationStandardLogDestination, error) {
	if len(in.AuthHeaderSealed) == 0 {
		in.AuthHeaderSealed = []byte{}
	}
	u, err := url.Parse(in.TargetURL)
	if !validStandardResourceIdentity(in.OrgID, in.ActorID, in.Name) || !slices.Contains(api.AllowedAppLogDrainKinds, in.Kind) || err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || len(in.TargetURL) > api.ApplicationStandardMaxDestinationURLBytes || len(in.AuthHeaderSealed) > api.ApplicationStandardMaxSealedCredentialBytes || strings.ContainsAny(in.TargetURL, "\r\n\x00") {
		return ApplicationStandardLogDestination{}, fmt.Errorf("invalid destination: use an HTTPS endpoint without userinfo, query credentials or fragment: %w", ErrInvalidArgument)
	}
	encoded, _ := json.Marshal(struct {
		Kind, URL string
		Sealed    []byte
	}{in.Kind, in.TargetURL, in.AuthHeaderSealed})
	hash := sha256.Sum256(encoded)
	return ApplicationStandardLogDestination{ApplicationStandardLogDestination: api.ApplicationStandardLogDestination{OrgID: in.OrgID, Name: in.Name, Kind: in.Kind, TargetURL: in.TargetURL, HasAuthHeader: len(in.AuthHeaderSealed) > 0, ConfigHash: hex.EncodeToString(hash[:]), CreatedBy: in.ActorID}, AuthHeaderSealed: slices.Clone(in.AuthHeaderSealed)}, nil
}

func prepareStandardPublisher(in ApplicationStandardPublisherCreate) (api.ApplicationStandardPublisher, error) {
	if !validStandardResourceIdentity(in.OrgID, in.ActorID, in.Name) || len(in.PublicKeyDER) > api.ApplicationStandardMaxPublisherKeyBytes {
		return api.ApplicationStandardPublisher{}, ErrInvalidArgument
	}
	key, err := x509.ParsePKIXPublicKey(in.PublicKeyDER)
	if err != nil {
		return api.ApplicationStandardPublisher{}, fmt.Errorf("invalid publisher public key: %w", ErrInvalidArgument)
	}
	pub, ok := key.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() || !pub.Curve.IsOnCurve(pub.X, pub.Y) {
		return api.ApplicationStandardPublisher{}, fmt.Errorf("publisher must use an ECDSA P-256 public key: %w", ErrInvalidArgument)
	}
	canonical, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return api.ApplicationStandardPublisher{}, err
	}
	hash := sha256.Sum256(canonical)
	return api.ApplicationStandardPublisher{OrgID: in.OrgID, Name: in.Name, PublicKeyDER: base64.StdEncoding.EncodeToString(canonical), Fingerprint: hex.EncodeToString(hash[:]), CreatedBy: in.ActorID}, nil
}

func standardResourceRefs(definition appstandards.Definition, field appstandards.Field) []string {
	var ids []string
	if rule, ok := definition[field]; ok {
		_ = json.Unmarshal(rule.Value, &ids)
	}
	return ids
}

func cloneStandardLogDestination(in ApplicationStandardLogDestination) ApplicationStandardLogDestination {
	in.AuthHeaderSealed = slices.Clone(in.AuthHeaderSealed)
	return in
}
