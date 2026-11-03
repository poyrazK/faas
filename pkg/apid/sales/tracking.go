package sales

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
)

const touchCookie = "__Host-gregale-referral"

func (s *Service) validLink(ctx context.Context, code string) (bool, error) {
	if !referrals.ValidCode(code) {
		return false, nil
	}
	link, err := s.Store.Link(ctx, code)
	if errors.Is(err, referrals.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !link.Active {
		return false, nil
	}
	if link.Kind == "sales" {
		if s.Account == nil {
			return false, nil
		}
		owner, err := s.Account(ctx, link.OwnerID)
		if errors.Is(err, state.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		_, member := s.Config.Member(owner.Email)
		return member, nil
	}
	return true, nil
}

func (s *Service) cookieCode(r *http.Request) string {
	if s.Key == nil {
		return ""
	}
	cookie, err := r.Cookie(touchCookie)
	if err != nil {
		return ""
	}
	return referrals.VerifyTouch(s.Key(), cookie.Value, time.Now(), api.ReferralTrackingTTL)
}

// Track lands on the API host so its host-only first-touch cookie survives all
// of the existing signup/OAuth flows, without a parent-domain cookie.
func (s *Service) Track(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if !s.Enabled() {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	valid, err := s.validLink(ctx, r.PathValue("code"))
	if err != nil {
		unavailable(w)
		return
	}
	if !valid {
		http.NotFound(w, r)
		return
	}
	existing := s.cookieCode(r)
	stillValid, err := s.validLink(ctx, existing)
	if err != nil {
		unavailable(w)
		return
	}
	if existing == "" || !stillValid {
		if s.Key == nil || len(s.Key()) < 32 {
			unavailable(w)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: touchCookie, Value: referrals.SignTouch(s.Key(), r.PathValue("code"), time.Now()), Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(api.ReferralTrackingTTL.Seconds())})
	}
	http.Redirect(w, r, "/signup", http.StatusSeeOther)
}

func (s *Service) SignupAttribution(r *http.Request) (*referrals.Attribution, error) {
	if !s.Enabled() {
		return nil, nil
	}
	code := s.cookieCode(r)
	if code == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	valid, err := s.validLink(ctx, code)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, nil
	}
	return &referrals.Attribution{Code: code, Policy: s.Config.Policy}, nil
}
