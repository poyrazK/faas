package consumerbilling

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

var creditPackID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func creditPackMinorAmount(pack api.ConsumerCreditPack) (int64, bool) {
	unit, err := billing.StripeMinorUnit(pack.Currency)
	if err != nil || pack.AmountMillicents <= 0 || pack.AmountMillicents > api.ConsumerCreditGrantMaxMillicents {
		return 0, false
	}
	minor := unit.FromMillicents(pack.AmountMillicents)
	back := minor
	for places := unit.Exponent; places < 5; places++ {
		back *= 10
	}
	return minor, minor > 0 && back == pack.AmountMillicents
}

func validateCreditPacks(settings *api.ConsumerCreditPackSettings) *api.Problem {
	invalid := func(detail string) *api.Problem {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid credit packs", detail)
	}
	settings.ReturnURL = strings.TrimSpace(settings.ReturnURL)
	if settings.Packs == nil {
		settings.Packs = []api.ConsumerCreditPack{}
	}
	if len(settings.Packs) > api.ConsumerCreditPacksMax {
		return invalid(fmt.Sprintf("configure at most %d packs", api.ConsumerCreditPacksMax))
	}
	if (len(settings.Packs) != 0 || settings.ReturnURL != "") && !api.ValidSignupURL(settings.ReturnURL) {
		return invalid("return_url must be an https URL")
	}
	seen := make(map[string]bool, len(settings.Packs))
	for i := range settings.Packs {
		pack := &settings.Packs[i]
		pack.Label = strings.TrimSpace(pack.Label)
		pack.Currency = strings.ToUpper(strings.TrimSpace(pack.Currency))
		if !creditPackID.MatchString(pack.ID) || seen[pack.ID] || pack.Label == "" || len(pack.Label) > api.ConsumerCreditPackLabelMaxBytes {
			return invalid(fmt.Sprintf("each pack needs a unique lowercase ID and a label of at most %d bytes", api.ConsumerCreditPackLabelMaxBytes))
		}
		if _, ok := creditPackMinorAmount(*pack); !ok {
			return invalid("each amount must be positive and exactly representable in Stripe's currency units")
		}
		seen[pack.ID] = true
	}
	return nil
}

func (s *Service) creditPackStore(w http.ResponseWriter) (state.APIConsumerCreditPackStore, bool) {
	store, ok := s.Host.Store().(state.APIConsumerCreditPackStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("credit packs are unavailable"))
	}
	return store, ok
}

func creditPackSettings(ctx context.Context, store state.APIConsumerCreditPackStore, app state.App) (api.ConsumerCreditPackSettings, error) {
	settings, err := store.GetConsumerCreditPackSettings(ctx, app.AccountID, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		return api.ConsumerCreditPackSettings{Packs: []api.ConsumerCreditPack{}}, nil
	}
	return settings, err
}

func (s *Service) GetCreditPackSettings(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.AppRoute(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.creditPackStore(w)
	if !ok {
		return
	}
	settings, err := creditPackSettings(r.Context(), store, app)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not read the credit packs"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, settings)
}

func (s *Service) PutCreditPackSettings(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.AppRoute(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.creditPackStore(w)
	if !ok {
		return
	}
	var settings api.ConsumerCreditPackSettings
	if err := httpkit.DecodeJSONSized(r, &settings, 16<<10); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if problem := validateCreditPacks(&settings); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if err := store.PutConsumerCreditPackSettings(r.Context(), app.AccountID, app.ID, settings); err != nil {
		api.WriteProblem(w, api.ErrInternal("could not save the credit packs"))
		return
	}
	s.Host.Audit(r.Context(), "api_consumer.credit_packs_configured", app.AccountID, map[string]any{"app_id": app.ID, "pack_count": len(settings.Packs)})
	httpkit.WriteJSON(w, http.StatusOK, settings)
}

func (s *Service) canBuyCredit(ctx context.Context, c caller) bool {
	return strings.HasPrefix(c.consumer.ExternalRef, "cus_") && s.Host.CreatorBills(ctx, c.app)
}

func (s *Service) CreditPacks(w http.ResponseWriter, r *http.Request) {
	c, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	store, ok := s.creditPackStore(w)
	if !ok {
		return
	}
	out := api.ConsumerCreditPacksResponse{Packs: []api.ConsumerCreditPack{}}
	if s.canBuyCredit(r.Context(), c) {
		settings, err := creditPackSettings(r.Context(), store, c.app)
		if err != nil {
			api.WriteProblem(w, api.ErrInternal("could not read the credit packs"))
			return
		}
		out.Packs = settings.Packs
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

func purchaseResponse(p state.APIConsumerCreditPurchase) api.ConsumerCreditPurchaseResponse {
	out := api.ConsumerCreditPurchaseResponse{ID: p.ID, PackID: p.Pack.ID, Currency: p.Pack.Currency,
		AmountMillicents: p.Pack.AmountMillicents, Status: p.Status, ExpiresAt: p.ExpiresAt, CreditGrantID: p.CreditGrantID()}
	return out
}

func (s *Service) GetCreditPurchase(w http.ResponseWriter, r *http.Request) {
	c, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	store, ok := s.creditPackStore(w)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("purchase_id"))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Credit purchase not found", ""))
		return
	}
	p, err := store.GetConsumerCreditPurchase(r.Context(), c.app.AccountID, c.app.ID, c.consumer.ID, id.String())
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Credit purchase not found", ""))
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("could not read the credit purchase"))
	default:
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusOK, purchaseResponse(p))
	}
}

func (s *Service) CreateCreditPurchase(w http.ResponseWriter, r *http.Request) {
	c, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	store, ok := s.creditPackStore(w)
	if !ok {
		return
	}
	var req api.CreateConsumerCreditPurchaseRequest
	if err := httpkit.DecodeJSONSized(r, &req, 4<<10); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	id, err := uuid.Parse(req.PurchaseID)
	if err != nil || id == uuid.Nil || !creditPackID.MatchString(req.PackID) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid purchase", "provide a purchase_id UUID and an offered pack_id"))
		return
	}
	req.PurchaseID = id.String()
	out, problem := s.openCreditPurchase(r.Context(), store, c, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, out)
}

func creditPurchaseConflict(detail string) *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeConsumerSelfServiceUnavailable, "Credit purchase unavailable", detail)
}

func (s *Service) openCreditPurchase(ctx context.Context, store state.APIConsumerCreditPackStore, c caller, req api.CreateConsumerCreditPurchaseRequest) (api.ConsumerCreditPurchaseResponse, *api.Problem) {
	p, err := store.GetConsumerCreditPurchase(ctx, c.app.AccountID, c.app.ID, c.consumer.ID, req.PurchaseID)
	if err == nil {
		if p.Pack.ID != req.PackID {
			return api.ConsumerCreditPurchaseResponse{}, creditPurchaseConflict("this purchase ID was used for a different pack")
		}
		if p.Status != state.ConsumerCreditPurchasePending {
			return purchaseResponse(p), nil
		}
	} else if !errors.Is(err, state.ErrNotFound) {
		return api.ConsumerCreditPurchaseResponse{}, api.ErrInternal("could not read the credit purchase")
	}
	if !s.canBuyCredit(ctx, c) {
		return api.ConsumerCreditPurchaseResponse{}, creditPurchaseConflict("this account cannot buy credit through Stripe")
	}
	key, live, problem := s.Host.StripeKey(ctx, c.app.AccountID)
	if problem != nil {
		return api.ConsumerCreditPurchaseResponse{}, creditPurchaseConflict("the Stripe connection is unavailable")
	}
	if errors.Is(err, state.ErrNotFound) {
		p, problem = s.startCreditPurchase(ctx, store, c, req, live)
		if problem != nil {
			return api.ConsumerCreditPurchaseResponse{}, problem
		}
	}
	if p.Status != state.ConsumerCreditPurchasePending {
		return purchaseResponse(p), nil
	}
	if p.Livemode != live || p.Customer != c.consumer.ExternalRef {
		return api.ConsumerCreditPurchaseResponse{}, creditPurchaseConflict("the Stripe customer or connection changed")
	}
	return s.creditCheckout(ctx, store, p, key)
}

func (s *Service) startCreditPurchase(ctx context.Context, store state.APIConsumerCreditPackStore, c caller, req api.CreateConsumerCreditPurchaseRequest, live bool) (state.APIConsumerCreditPurchase, *api.Problem) {
	settings, err := creditPackSettings(ctx, store, c.app)
	if err != nil {
		return state.APIConsumerCreditPurchase{}, api.ErrInternal("could not read the credit packs")
	}
	index := slices.IndexFunc(settings.Packs, func(pack api.ConsumerCreditPack) bool { return pack.ID == req.PackID })
	if index < 0 {
		return state.APIConsumerCreditPurchase{}, creditPurchaseConflict("the selected pack is unavailable")
	}
	pack := settings.Packs[index]
	minor, ok := creditPackMinorAmount(pack)
	if !ok {
		return state.APIConsumerCreditPurchase{}, creditPurchaseConflict("the selected pack cannot be charged in Stripe")
	}
	p, err := store.StartConsumerCreditPurchase(ctx, state.APIConsumerCreditPurchase{
		ID: req.PurchaseID, AccountID: c.app.AccountID, AppID: c.app.ID, ConsumerID: c.consumer.ID, Pack: pack,
		Customer: c.consumer.ExternalRef, Livemode: live, AmountMinor: minor, ReturnURL: settings.ReturnURL,
		ExpiresAt: s.now().Add(api.ConsumerCreditPurchaseTTL).Truncate(time.Second),
	})
	if errors.Is(err, state.ErrConflict) {
		return state.APIConsumerCreditPurchase{}, creditPurchaseConflict("finish the open purchase or use available credit before buying again")
	}
	if err != nil {
		return state.APIConsumerCreditPurchase{}, api.ErrInternal("could not start the credit purchase")
	}
	return p, nil
}

func (s *Service) creditCheckout(ctx context.Context, store state.APIConsumerCreditPackStore, p state.APIConsumerCreditPurchase, key string) (api.ConsumerCreditPurchaseResponse, *api.Problem) {
	var session stripeinvoice.CheckoutSession
	var err error
	if p.SessionID != "" {
		session, err = s.stripe().GetCreditPackCheckout(ctx, key, p.SessionID)
	} else {
		// Stripe requires at least 30 minutes before a new session expires.
		if !p.ExpiresAt.After(s.now().Add(31 * time.Minute)) {
			if err := store.ExpireConsumerCreditPurchase(ctx, p.ID, ""); err != nil {
				return api.ConsumerCreditPurchaseResponse{}, api.ErrInternal("could not expire the credit purchase")
			}
			return api.ConsumerCreditPurchaseResponse{}, creditPurchaseConflict("this checkout expired; start a new purchase")
		}
		returnURL, parseErr := url.Parse(p.ReturnURL)
		if parseErr != nil {
			return api.ConsumerCreditPurchaseResponse{}, api.ErrInternal("invalid saved credit return URL")
		}
		query := returnURL.Query()
		query.Set("credit_purchase_id", p.ID)
		returnURL.RawQuery = query.Encode()
		session, err = s.stripe().CreateCreditPackCheckout(ctx, key, stripeinvoice.CreditPackCheckoutParams{
			PurchaseID: p.ID, Customer: p.Customer, Label: p.Pack.Label, Currency: p.Pack.Currency, AmountMinor: p.AmountMinor,
			ReturnURL: returnURL.String(), ExpiresAt: p.ExpiresAt,
		})
		if err != nil {
			return api.ConsumerCreditPurchaseResponse{}, creditStripeProblem()
		}
		if !matchesCreditCheckout(p, session) || session.Status != "open" || api.ProviderHostedURL(session.URL) == "" {
			return api.ConsumerCreditPurchaseResponse{}, creditStripeProblem()
		}
		p, err = store.SetConsumerCreditPurchaseCheckout(ctx, p.ID, session.ID, s.now())
		if err != nil {
			return api.ConsumerCreditPurchaseResponse{}, api.ErrInternal("could not save the checkout; retry with the same purchase ID")
		}
	}
	if err != nil || !matchesCreditCheckout(p, session) {
		return api.ConsumerCreditPurchaseResponse{}, creditStripeProblem()
	}
	out := purchaseResponse(p)
	if session.Status == "open" {
		out.CheckoutURL = api.ProviderHostedURL(session.URL)
	}
	return out, nil
}

func creditStripeProblem() *api.Problem {
	return api.NewProblem(http.StatusBadGateway, api.CodeBillingProviderUnavailable, "Payment provider unavailable", "could not open Stripe Checkout; retry with the same purchase ID")
}

func matchesCreditCheckout(p state.APIConsumerCreditPurchase, session stripeinvoice.CheckoutSession) bool {
	return strings.HasPrefix(session.ID, "cs_") && (p.SessionID == "" || p.SessionID == session.ID) &&
		session.ClientReferenceID == p.ID && session.Mode == "payment" && session.Customer == p.Customer &&
		session.Livemode == p.Livemode && strings.EqualFold(session.Currency, p.Pack.Currency) && session.AmountTotal == p.AmountMinor
}
