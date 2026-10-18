package product

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state"
)

type ownerAction struct {
	Action       string `json:"action"`
	Tenant       string `json:"tenant"`
	Period       string `json:"period"`
	Plan         string `json:"plan,omitempty"`
	Revision     string `json:"revision,omitempty"`
	Suspended    bool   `json:"suspended,omitempty"`
	Organization string `json:"organization,omitempty"`
	ID           string `json:"id,omitempty"`
	Hash         string `json:"hash,omitempty"`
	Operation    string `json:"operation,omitempty"`
	Document     string `json:"document,omitempty"`
	Amount       string `json:"amount,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Reference    string `json:"reference,omitempty"`
}

func (a ownerAction) customer() api.ProductCustomer {
	return api.ProductCustomer{TenantID: a.Tenant, Plan: a.Plan, Revision: a.Revision, Suspended: a.Suspended, AuthOrganizationID: a.Organization}
}
func (a ownerAction) scope(c *ownerContext) state.ProductBillingScope {
	return state.ProductBillingScope{AccountID: c.account.ID, AppID: c.app.ID, TenantID: a.Tenant, Period: a.Period}
}

func ownerParseForm(w http.ResponseWriter, r *http.Request, allowed []string) (url.Values, error) {
	return ownerParseFormLimit(w, r, allowed, api.ProductControlOwnerFormMaxBytes)
}

func ownerParseFormLimit(w http.ResponseWriter, r *http.Request, allowed []string, limit int64) (url.Values, error) {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/x-www-form-urlencoded" || r.URL.RawQuery != "" {
		return nil, state.ErrInvalidArgument
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := r.ParseForm(); err != nil {
		return nil, state.ErrInvalidArgument
	}
	for key, values := range r.PostForm {
		if !ownerAllowed(key, allowed) || len(values) != 1 || !utf8.ValidString(values[0]) {
			return nil, state.ErrInvalidArgument
		}
	}
	return r.PostForm, nil
}

func ownerParseReview(w http.ResponseWriter, r *http.Request) (ownerAction, error) {
	allowed := []string{"csrf_token", "action", "tenant", "period", "plan", "revision", "suspended", "organization", "id", "hash", "operation", "document", "amount", "reason", "reference"}
	f, err := ownerParseForm(w, r, allowed)
	if err != nil {
		return ownerAction{}, err
	}
	fields := []string{"csrf_token", "action", "tenant", "period"}
	switch f.Get("action") {
	case "plan":
		fields = append(fields, "plan", "revision", "suspended", "organization")
	case "finalize":
	case "waive":
		fields = append(fields, "operation", "reason")
	case "credit":
		fields = append(fields, "operation", "amount", "reason")
	case "handoff":
		fields = append(fields, "document", "reference")
	default:
		return ownerAction{}, state.ErrInvalidArgument
	}
	for key := range f {
		if !ownerAllowed(key, fields) {
			return ownerAction{}, state.ErrInvalidArgument
		}
	}
	if f.Has("suspended") && f.Get("suspended") != "true" && f.Get("suspended") != "false" {
		return ownerAction{}, state.ErrInvalidArgument
	}
	a := ownerAction{Action: f.Get("action"), Tenant: f.Get("tenant"), Period: f.Get("period"), Plan: f.Get("plan"), Revision: f.Get("revision"), Suspended: f.Get("suspended") == "true", Organization: f.Get("organization"), Operation: f.Get("operation"), Document: f.Get("document"), Amount: f.Get("amount"), Reason: f.Get("reason"), Reference: f.Get("reference")}
	return a, ownerValidateAction(a, false)
}

func ownerValidateAction(a ownerAction, confirmed bool) error {
	if !ownerUUID(a.Tenant) || productcontrol.ValidatePeriod(a.Period) != nil {
		return state.ErrInvalidArgument
	}
	switch a.Action {
	case "plan":
		if a.Plan == "" || len(a.Plan) > api.ProductControlMaxLabelBytes || (a.Organization != "" && !ownerUUID(a.Organization)) || (confirmed && (!ownerUUID(a.ID) || len(a.Hash) != 64 || len(a.Revision) != 64)) {
			return state.ErrInvalidArgument
		}
	case "finalize":
		if confirmed && len(a.Hash) != 64 {
			return state.ErrInvalidArgument
		}
	case "waive":
		if !ownerUUID(a.Operation) || !ownerText(a.Reason, api.ProductControlBillingReasonMaxBytes) {
			return state.ErrInvalidArgument
		}
	case "credit":
		amount, err := strconv.ParseInt(a.Amount, 10, 64)
		if err != nil || amount <= 0 || amount > api.ProductControlMaxUnitPriceMillicents || strconv.FormatInt(amount, 10) != a.Amount || !ownerUUID(a.Operation) || !ownerText(a.Reason, api.ProductControlBillingReasonMaxBytes) || (confirmed && !ownerUUID(a.ID)) {
			return state.ErrInvalidArgument
		}
	case "handoff":
		if !ownerUUID(a.Document) || !ownerText(a.Reference, api.ProductControlInvoiceReferenceMaxBytes) {
			return state.ErrInvalidArgument
		}
	default:
		return state.ErrInvalidArgument
	}
	return nil
}

func ownerText(value string, limit int) bool {
	return value != "" && len(value) <= limit && utf8.ValidString(value) && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}

func ownerPayload(a ownerAction) string {
	raw, _ := json.Marshal(a)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func ownerDecodePayload(payload string) (ownerAction, error) {
	var a ownerAction
	raw, err := base64.RawURLEncoding.Strict().DecodeString(payload)
	if err != nil {
		return a, state.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&a) != nil || d.Decode(new(any)) != io.EOF || ownerPayload(a) != payload {
		return a, state.ErrInvalidArgument
	}
	return a, ownerValidateAction(a, true)
}

func ownerCookie(action string) string { return "gregale_pc_" + action }
func (s *Service) ownerToken(w http.ResponseWriter, c *ownerContext, action, subject string) (string, error) {
	if s.OwnerSessions == nil {
		return "", state.ErrNotFound
	}
	token, err := middleware.IssueForAuthenticatedNamed(s.OwnerSessions(), "product-owner/"+action, subject, ownerCookie(action))
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{Name: ownerCookie(action), Value: token, Path: c.data.BaseURL, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
	return token, nil
}

func (s *Service) ownerVerify(r *http.Request, c *ownerContext, action, subject string) error {
	if s.OwnerSessions == nil {
		return middleware.ErrCSRFInvalid
	}
	return middleware.VerifyAuthenticatedNamed(s.OwnerSessions(), r, "product-owner/"+action, subject, ownerCookie(action))
}

func ownerConfirmationSubject(c *ownerContext, payload string) string {
	return c.account.ID + "/" + c.app.ID + "/" + productcontrolOwnerHash(payload)
}
