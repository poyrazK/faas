package state

import (
	"context"

	"github.com/google/uuid"
)

type lineageControlActorKey struct{}
type lineageControlActor struct{ AccountID, Kind, ID, CredentialType string }

// WithEventExecutionLineageControlActor carries server-resolved identity only.
// Public request DTOs never accept these fields. AccountID binds the attribution
// to the authenticated account passed to the control operation.
func WithEventExecutionLineageControlActor(ctx context.Context, account, kind, id, credential string) context.Context {
	return context.WithValue(ctx, lineageControlActorKey{}, lineageControlActor{account, kind, id, credential})
}

func eventExecutionLineageControlActor(ctx context.Context, account string) (lineageControlActor, error) {
	actor, ok := ctx.Value(lineageControlActorKey{}).(lineageControlActor)
	if !ok {
		return lineageControlActor{Kind: "internal", ID: "store", CredentialType: "internal"}, nil
	}
	owner, err := uuid.Parse(actor.AccountID)
	expected, expectedErr := uuid.Parse(account)
	id, idErr := uuid.Parse(actor.ID)
	if err != nil || expectedErr != nil || idErr != nil || owner == uuid.Nil || owner != expected || id == uuid.Nil {
		return lineageControlActor{}, ErrInvalidArgument
	}
	valid := actor.Kind == "account" && actor.CredentialType == "session" && id == owner
	if actor.Kind == "credential" {
		valid = actor.CredentialType == "api_key" || actor.CredentialType == "deploy_token" || actor.CredentialType == "platform_tenant_token"
	}
	if !valid {
		return lineageControlActor{}, ErrInvalidArgument
	}
	actor.AccountID, actor.ID = owner.String(), id.String()
	return actor, nil
}
