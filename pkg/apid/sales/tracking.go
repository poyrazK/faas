package sales

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
)

const touchCookie = "__Host-gregale-referral-code"

type submittedReferralKey struct{}

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
	return referrals.VerifyTouch(s.Key(), cookie.Value, time.Now(), api.ReferralSelectionTTL)
}

// SelectCode preserves an explicitly entered code through an OAuth redirect.
// There is no referral URL and no automatic click attribution.
func (s *Service) SelectCode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.Enabled() {
		http.NotFound(w, r)
		return
	}
	var input struct {
		Code *string `json:"referral_code"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Code == nil {
		invalid(w, "provide a referral code")
		return
	}
	code := referrals.NormalizeCode(*input.Code)
	if _, ok := s.validateSubmittedCode(w, r, code); !ok {
		return
	}
	if s.Key == nil || len(s.Key()) < 32 {
		unavailable(w)
		return
	}
	cookie := &http.Cookie{Name: touchCookie, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1}
	if code != "" {
		cookie.Value = referrals.SignTouch(s.Key(), code, time.Now())
		cookie.MaxAge = int(api.ReferralSelectionTTL.Seconds())
	}
	http.SetCookie(w, cookie)
	writeJSON(w, map[string]bool{"accepted": true})
}

func (s *Service) SignupAttribution(r *http.Request) (*referrals.Attribution, error) {
	if entered, ok := r.Context().Value(submittedReferralKey{}).(*referrals.Attribution); ok {
		return entered, nil
	}
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

func (s *Service) validateSubmittedCode(w http.ResponseWriter, r *http.Request, code string) (*referrals.Attribution, bool) {
	if code == "" {
		return nil, true
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	valid, err := false, error(nil)
	if s.Enabled() {
		valid, err = s.validLink(ctx, code)
	}
	if err != nil {
		unavailable(w)
		return nil, false
	}
	if !valid {
		api.WriteProblem(w, api.NewProblem(400, "referral_code_invalid", "Invalid referral code", "This referral code is invalid or inactive. Check the code or leave it blank."))
		return nil, false
	}
	return &referrals.Attribution{Code: code, Policy: s.Config.Policy}, true
}

// WithSignupCode extracts the optional field before the existing strict
// credential decoder. All other fields retain their original validation.
// Validation happens before the account lookup, preserving anti-enumeration.
func (s *Service) WithSignupCode(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code, present, err := readSubmittedCode(w, r)
		if err != nil {
			invalid(w, "could not decode signup body")
			return
		}
		var attribution *referrals.Attribution
		if present {
			var ok bool
			attribution, ok = s.validateSubmittedCode(w, r, referrals.NormalizeCode(code))
			if !ok {
				return
			}
		}
		// Native signup uses only its submitted field; a saved selection is
		// reserved for OAuth callbacks and must not credit an unrelated signup.
		r = r.WithContext(context.WithValue(r.Context(), submittedReferralKey{}, attribution))
		next.ServeHTTP(w, r)
	})
}

func readSubmittedCode(w http.ResponseWriter, r *http.Request) (string, bool, error) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body map[string]json.RawMessage
		if err := httpkit.DecodeJSON(r, &body); err != nil {
			return "", false, err
		}
		value, present := body["referral_code"]
		var code string
		if present {
			if err := json.Unmarshal(value, &code); err != nil {
				return "", false, err
			}
			delete(body, "referral_code")
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return "", false, err
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		r.ContentLength = int64(len(raw))
		return code, present, nil
	}
	r.Body = http.MaxBytesReader(w, r.Body, httpkit.DefaultJSONBodyMaxBytes)
	if err := r.ParseForm(); err != nil {
		return "", false, err
	}
	_, present := r.PostForm["referral_code"]
	return r.PostForm.Get("referral_code"), present, nil
}
