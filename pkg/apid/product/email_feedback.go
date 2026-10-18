package product

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/productemail"
)

func ValidEmailWebhookSecret(value string) bool {
	if value == "" {
		return true
	}
	if !strings.HasPrefix(value, "whsec_") {
		return false
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "whsec_"))
	return err == nil && len(key) >= 24 && len(key) <= 64
}
func (s *Service) EmailFeedback(w http.ResponseWriter, r *http.Request) {
	if s.EmailWebhookSecret == "" {
		api.WriteProblem(w, api.ErrCapacity("Product email feedback is not configured"))
		return
	}
	if _, err := ownerQuery(r); err != nil {
		api.WriteProblem(w, api.ErrValidation("Email feedback does not accept query selectors"))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, api.ProductControlEmailFeedbackBodyMaxBytes))
	if err != nil || !s.emailFeedbackSignature(r, body) {
		api.WriteProblem(w, api.ErrValidation("Invalid email feedback signature or body"))
		return
	}
	now := time.Now().UTC()
	f, recognized, err := decodeEmailFeedback(body, now)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid email feedback event"))
		return
	}
	if !recognized {
		w.WriteHeader(http.StatusOK)
		return
	}
	repo, err := s.ownerEmailRepository()
	if err == nil {
		ctx, cancel := context.WithTimeout(r.Context(), api.ProductControlEmailOwnerMaxDuration)
		defer cancel()
		err = repo.Feedback(ctx, f, now)
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("Email feedback could not be retained; retry the same event"))
		return
	}
	w.WriteHeader(http.StatusOK)
}
func (s *Service) emailFeedbackSignature(r *http.Request, body []byte) bool {
	for _, name := range []string{mail.ResendIDHeader, mail.ResendTimestampHeader, mail.ResendSignatureHeader} {
		if len(r.Header.Values(name)) != 1 || len(r.Header.Get(name)) > api.ProductControlEmailFeedbackHeaderMaxBytes {
			return false
		}
	}
	return mail.VerifyResendSignature(body, r.Header.Get(mail.ResendSignatureHeader), s.EmailWebhookSecret, r.Header.Get(mail.ResendIDHeader), r.Header.Get(mail.ResendTimestampHeader), api.ProductControlEmailFeedbackTolerance) == nil
}
func decodeEmailFeedback(body []byte, now time.Time) (productemail.Feedback, bool, error) {
	var event struct {
		Type      string    `json:"type"`
		CreatedAt time.Time `json:"created_at"`
		Data      struct {
			EmailID string `json:"email_id"`
			Bounce  struct {
				Type string `json:"type"`
			} `json:"bounce"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &event) != nil {
		return productemail.Feedback{}, false, errors.New("invalid envelope")
	}
	f := productemail.Feedback{ProviderID: event.Data.EmailID, OccurredAt: event.CreatedAt}
	switch event.Type {
	case "email.delivered":
		f.Kind = "delivered"
	case "email.complained":
		f.Kind = "complained"
	case "email.bounced":
		f.Kind = "bounced"
		f.HardBounce = strings.EqualFold(event.Data.Bounce.Type, "Permanent")
	default:
		return f, false, nil
	}
	if !canonicalEmailID(f.ProviderID) || f.OccurredAt.IsZero() || !f.OccurredAt.After(now.Add(-api.ProductControlNotificationWindow)) || f.OccurredAt.After(now.Add(api.ProductControlEmailFeedbackTolerance)) {
		return f, true, errors.New("invalid receipt or event time")
	}
	return f, true, nil
}
