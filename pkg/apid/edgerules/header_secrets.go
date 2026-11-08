package edgerules

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// sealHeaderSecrets seals the request header values of action that are kept
// sealed (ADR-20261108110000): ops marked secret and literals on
// credential-named headers. A value is sealed to the edge domain, stored as
// an edge_rule_header_secrets row of app, and replaced by the row's id. An op
// sent back with its secret_id and no value keeps that row, which must belong
// to app; a redacted op (a credential stored before sealing) seals the value
// previous holds for the same header. previous is the stored rule on update,
// nil on create. action is changed in place.
func (s *Service) sealHeaderSecrets(ctx context.Context, app state.App, action *state.EdgeRuleAction, previous *state.EdgeRule) *api.Problem {
	if action == nil || action.Headers == nil {
		return nil
	}
	ops := append([]state.EdgeRuleHeaderOp(nil), action.Headers.RequestHeaders...)
	for i := range ops {
		op := &ops[i]
		if !api.EdgeRuleHeaderOpSealed(api.EdgeRuleHeaderOp{Name: op.Name, Action: op.Action, Template: op.Template, Secret: op.Secret, SecretID: op.SecretID}) {
			if op.Redacted && op.Value == "" {
				return api.ErrValidation(fmt.Sprintf("header %q is redacted; send its value", op.Name))
			}
			op.Redacted = false
			continue
		}
		value := op.Value
		if value == "" && op.Redacted && op.SecretID == "" {
			stored, ok := storedHeaderLiteral(previous, op.Name, op.Action)
			if !ok {
				return api.ErrValidation(fmt.Sprintf("header %q is redacted and no stored value matches it; send its value", op.Name))
			}
			value = stored
		}
		op.Redacted = false
		switch {
		case value != "":
			id, prob := s.storeHeaderSecret(ctx, app, value)
			if prob != nil {
				return prob
			}
			op.Value, op.Secret, op.SecretID = "", true, id
		case op.SecretID != "":
			if prob := s.headerSecretProblem(ctx, app, op.Name, op.SecretID); prob != nil {
				return prob
			}
			op.Secret = true
		case op.Secret:
			return api.ErrValidation(fmt.Sprintf("secret header %q needs a value, or the secret_id of its sealed value", op.Name))
		}
	}
	headers := *action.Headers
	headers.RequestHeaders = ops
	action.Headers = &headers
	return nil
}

// storedHeaderLiteral is the plaintext value previous stores for a request
// header op on name with action, if any.
func storedHeaderLiteral(previous *state.EdgeRule, name, action string) (string, bool) {
	if previous == nil || previous.Action.Headers == nil {
		return "", false
	}
	for _, op := range previous.Action.Headers.RequestHeaders {
		if op.Name == name && op.Action == action && op.Value != "" && op.SecretID == "" && !op.Template {
			return op.Value, true
		}
	}
	return "", false
}

func (s *Service) headerSecretStore() (state.EdgeRuleHeaderSecretStore, *api.Problem) {
	store, ok := s.Host.Store().(state.EdgeRuleHeaderSecretStore)
	if !ok || s.SealHeaderValue == nil {
		return nil, api.ErrCapacity("sealed header values are unavailable on this deployment")
	}
	return store, nil
}

func (s *Service) storeHeaderSecret(ctx context.Context, app state.App, value string) (string, *api.Problem) {
	store, prob := s.headerSecretStore()
	if prob != nil {
		return "", prob
	}
	sealed, err := s.SealHeaderValue(value)
	if err != nil {
		s.Host.Logger().Error("seal edge rule header value failed", "app", app.ID, "err", err)
		return "", api.ErrCapacity("could not seal the header value")
	}
	row, err := store.CreateEdgeRuleHeaderSecret(ctx, app.AccountID, app.ID, sealed)
	if err != nil {
		s.Host.Logger().Error("store edge rule header secret failed", "app", app.ID, "err", err)
		return "", api.ErrCapacity("could not store the header value")
	}
	return row.ID, nil
}

// headerSecretProblem refuses a secret_id that does not name a sealed value
// of app; ids of other apps answer the same, so nothing about them leaks.
func (s *Service) headerSecretProblem(ctx context.Context, app state.App, name, id string) *api.Problem {
	store, prob := s.headerSecretStore()
	if prob != nil {
		return prob
	}
	rows, err := store.EdgeRuleHeaderSecretsByID(ctx, app.ID, []string{id})
	if err != nil {
		return api.ErrCapacity("could not read the sealed header value")
	}
	if len(rows) == 0 {
		return api.ErrValidation(fmt.Sprintf("header %q secret_id %q is not a sealed value of this app; send the value instead", name, id))
	}
	return nil
}

// RedactHeaderSecrets returns action as responses show it: a credential-named
// request header value stored before sealing is left out and marked
// redacted. Sealed ops already carry no value.
func RedactHeaderSecrets(action state.EdgeRuleAction) state.EdgeRuleAction {
	if action.Headers == nil {
		return action
	}
	var ops []state.EdgeRuleHeaderOp
	for i, op := range action.Headers.RequestHeaders {
		if op.Value == "" || op.SecretID != "" || op.Template || op.Action == "remove" ||
			(!op.Secret && !api.EdgeRuleHeaderNameIsCredential(op.Name)) {
			continue
		}
		if ops == nil {
			ops = append([]state.EdgeRuleHeaderOp(nil), action.Headers.RequestHeaders...)
		}
		ops[i].Value, ops[i].Redacted = "", true
	}
	if ops == nil {
		return action
	}
	headers := *action.Headers
	headers.RequestHeaders = ops
	action.Headers = &headers
	return action
}

// SecretHeaderOpsProblem refuses sealed header fields in an action that
// cannot hold them yet (environment edge policies, ADR-20261108110000).
func SecretHeaderOpsProblem(kind string, action state.EdgeRuleAction) *api.Problem {
	if kind != string(state.EdgeRuleKindHeaders) || action.Headers == nil {
		return nil
	}
	for _, op := range action.Headers.RequestHeaders {
		if op.Secret || op.SecretID != "" || op.Redacted {
			return api.ErrValidation("environment policies do not accept secret header values yet; set the header on an application edge rule")
		}
	}
	return nil
}
