package apidgrpc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
)

// OAuth consumer enrollment refusals (ADR-20261124094745).
var (
	// ErrOAuthConsumersOff: the app does not enroll OAuth callers.
	ErrOAuthConsumersOff = errors.New("apidgrpc: OAuth consumers are off for the app")
	// ErrOAuthConsumerSignupLimit: the app took its sign-ups for the day.
	ErrOAuthConsumerSignupLimit = errors.New("apidgrpc: the app took its sign-ups for the day")
)

// UpgradeLink returns the consumer's upgrade page link and when it
// expires, or "" when the app offers no upgrade (or apid predates the
// RPC). Any error means apid could not answer.
func (c OAuthConsumers) UpgradeLink(ctx context.Context, accountID, appID, consumerID string) (string, time.Time, error) {
	resp, err := c.Auth.cli.ConsumerUpgradeLink(ctx, &apidpb.ConsumerUpgradeLinkRequest{AccountId: accountID, AppId: appID, ConsumerId: consumerID})
	switch status.Code(err) {
	case codes.OK:
		return resp.GetUrl(), time.UnixMilli(resp.GetExpiresAtUnixMs()), nil
	case codes.Unimplemented, codes.NotFound:
		return "", time.Time{}, nil
	}
	return "", time.Time{}, fmt.Errorf("apidgrpc: ConsumerUpgradeLink: %w", err)
}

// OAuthConsumers is how a gateway turns a verified OAuth caller of an MCP
// server into the app's API consumer: it asks apid, the only writer of
// consumers.
type OAuthConsumers struct {
	Auth *AuthClientImpl
}

// EnrollOAuthConsumer returns the consumer issuer and subject are on the
// app, and whether it is active. ErrOAuthConsumersOff and
// ErrOAuthConsumerSignupLimit are refusals; any other error means apid could
// not answer.
func (c OAuthConsumers) EnrollOAuthConsumer(ctx context.Context, accountID, appID, issuer, subject, email string) (string, bool, error) {
	resp, err := c.Auth.cli.EnrollOAuthConsumer(ctx, &apidpb.EnrollOAuthConsumerRequest{
		AccountId: accountID, AppId: appID, Issuer: issuer, Subject: subject, Email: email})
	switch status.Code(err) {
	case codes.OK:
		return resp.GetConsumerId(), resp.GetActive(), nil
	case codes.FailedPrecondition, codes.Unimplemented:
		// An apid older than the RPC enrolls nobody (ADR-20261124094745).
		return "", false, ErrOAuthConsumersOff
	case codes.ResourceExhausted:
		return "", false, ErrOAuthConsumerSignupLimit
	}
	return "", false, fmt.Errorf("apidgrpc: EnrollOAuthConsumer: %w", err)
}
