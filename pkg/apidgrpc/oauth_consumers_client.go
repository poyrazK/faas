package apidgrpc

import (
	"context"
	"errors"
	"fmt"

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
	case codes.FailedPrecondition:
		return "", false, ErrOAuthConsumersOff
	case codes.ResourceExhausted:
		return "", false, ErrOAuthConsumerSignupLimit
	}
	return "", false, fmt.Errorf("apidgrpc: EnrollOAuthConsumer: %w", err)
}
