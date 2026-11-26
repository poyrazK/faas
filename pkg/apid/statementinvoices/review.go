package statementinvoices

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type statementInvoicePreparation struct {
	provider   provider
	connection connection
	proposed   state.StatementInvoiceExport
	terms      state.StatementInvoiceExport
}

func (s *Service) prepareStatementInvoice(ctx context.Context, acct state.Account, store state.StatementInvoiceExportStore, src statementInvoiceSource, name string) (statementInvoicePreparation, *api.Problem) {
	var out statementInvoicePreparation
	p := s.provider(name)
	if p == nil {
		return out, providerProblem(http.StatusUnprocessableEntity, "Unknown billing provider", "provider must be stripe, paddle or lago")
	}
	prior, err := store.GetStatementInvoiceExport(ctx, acct.ID, src.kind, src.statementID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return out, api.ErrInternal("could not read the statement invoice export")
	}
	hasPrior := err == nil
	if hasPrior && prior.Provider != name && !prior.Retargetable() {
		return out, exportConflict("the statement was already exported to " + prior.Provider)
	}
	if hasPrior && prior.Status == state.StatementInvoiceExportCompleted {
		if src.draftLivemode != nil {
			if prior.Collection != state.CollectionNone {
				return out, exportConflict("this statement already has an export configured to collect")
			}
			if prior.Livemode != *src.draftLivemode {
				return out, draftModeConflict()
			}
			if !src.draftConnectionAt.IsZero() && prior.Customer != src.requestedCustomer {
				return out, exportConflict("the Stripe customer differs from the reviewed export")
			}
		}
		if src.expectedReview != "" && src.requestedCustomer != prior.Customer {
			return out, statementInvoiceReviewChanged()
		}
		out.terms = prior
		return out, nil
	}
	if src.existingHandoff != "" && (!hasPrior || prior.ProviderInvoiceID != src.existingHandoff) {
		return out, exportConflict("the statement was already handed off to another invoice")
	}
	resolveDraftCustomer := src.draftLivemode != nil && src.requestedCustomer == ""
	var customer string
	if !resolveDraftCustomer {
		var problem *api.Problem
		customer, problem = p.customer(src.requestedCustomer, src.externalRef)
		if problem != nil {
			return out, problem
		}
	}
	if problem := p.currencyProblem(src.invoice.Currency); problem != nil {
		return out, problem
	}
	if src.existingHandoff == "" {
		if problem := s.checkHandoffAllowed(ctx, acct, src); problem != nil {
			return out, problem
		}
	}
	conn, problem := s.openConnection(ctx, store, acct.ID, name, p)
	if problem != nil {
		return out, problem
	}
	if src.draftLivemode != nil {
		if conn.collection != state.CollectionNone {
			return out, draftCollectionConflict()
		}
		if conn.livemode != *src.draftLivemode {
			return out, draftModeConflict()
		}
	}
	if !src.draftConnectionAt.IsZero() && !conn.updatedAt.Equal(src.draftConnectionAt) {
		return out, exportConflict("the Stripe connection changed; review the draft again")
	}
	if resolveDraftCustomer {
		if prior.Provider == name && !prior.Retargetable() {
			customer = prior.Customer
		} else {
			linked, problem := s.linkedStripeCustomer(ctx, acct.ID, src.consumerID, conn.stripeAccountID, conn.livemode)
			if problem != nil {
				return out, problem
			}
			customer, problem = p.customer(linked, src.externalRef)
			if problem != nil {
				return out, problem
			}
		}
	}
	if hasPrior && !prior.Retargetable() && prior.Customer != customer {
		return out, exportConflict("an export for this statement already targets another customer")
	}
	proposed := state.StatementInvoiceExport{
		AccountID: acct.ID, SourceKind: src.kind, StatementID: src.statementID, Provider: name, Customer: customer,
		Currency: src.invoice.Currency, AmountMinor: src.invoice.TotalMinor, Livemode: conn.livemode,
		Collection: conn.collection, DaysUntilDue: conn.daysUntilDue, AutomaticTax: conn.automaticTax,
	}
	terms := proposed
	if hasPrior && !prior.Retargetable() {
		if prior.Livemode != conn.livemode {
			return out, exportConflict("the export started with a " + p.title() + " key in the other mode; reconnect that key to finish it")
		}
		terms = prior
	}
	return statementInvoicePreparation{provider: p, connection: conn, proposed: proposed, terms: terms}, nil
}

func statementInvoiceReview(e state.StatementInvoiceExport, conn connection) api.ProductInvoiceExportReview {
	review := api.ProductInvoiceExportReview{
		StatementID: e.StatementID, Provider: e.Provider, Customer: e.Customer, Currency: e.Currency,
		AmountMinor: e.AmountMinor, Livemode: e.Livemode, Collection: e.Collection,
		DaysUntilDue: e.DaysUntilDue, AutomaticTax: e.AutomaticTax,
	}
	raw, _ := json.Marshal(struct {
		Account, SourceKind string
		ConnectionUpdatedAt time.Time
		Terms               api.ProductInvoiceExportReview
	}{e.AccountID, e.SourceKind, conn.updatedAt.UTC(), review})
	digest := sha256.Sum256(raw)
	review.Hash = hex.EncodeToString(digest[:])
	return review
}

func matchesStatementInvoiceReview(src statementInvoiceSource, e state.StatementInvoiceExport, conn connection) bool {
	return src.expectedReview == "" || src.expectedReview == statementInvoiceReview(e, conn).Hash
}

func statementInvoiceReviewChanged() *api.Problem {
	return exportConflict("The invoice terms or connection changed. Review the export again.")
}
