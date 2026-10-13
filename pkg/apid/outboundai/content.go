package outboundai

import (
	"encoding/base64"
	"errors"
	"net/http"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// GetRequestContent returns one logged call's captured bodies (ADR-996).
// A call without a capture is not found.
func (s *Service) GetRequestContent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	requestID := r.PathValue("request")
	if _, err := uuid.Parse(requestID); err != nil {
		problem(w, http.StatusBadRequest, api.CodeValidation, "Request ID must be a UUID")
		return
	}
	store, ok := s.Host.Store().(state.OutboundAIContentStore)
	if !ok {
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI content capture is unavailable")
		return
	}
	content, err := store.GetOutboundAIRequestContent(r.Context(), acct.ID, integrationID, requestID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "captured AI request content not found")
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI request content is unavailable")
	default:
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusOK, api.OutboundAIRequestContent{
			RequestID: content.LogID, CreatedAt: content.CreatedAt, RequestPath: optionalString(content.RequestPath),
			Request:  contentBody(content.RequestBody, content.RequestTruncated, ""),
			Response: contentBody(content.ResponseBody, content.ResponseTruncated, content.ResponseContentType),
		})
	}
}

// contentBody returns text when the captured bytes are UTF-8, ignoring a
// character the capture cut in half, and base64 otherwise.
func contentBody(body []byte, truncated bool, contentType string) api.OutboundAIContentBody {
	out := api.OutboundAIContentBody{Truncated: truncated}
	if contentType != "" {
		out.ContentType = &contentType
	}
	text := body
	if truncated {
		text = trimPartialRune(body)
	}
	if utf8.Valid(text) {
		out.Body, out.Encoding = string(text), api.OutboundAIContentEncodingUTF8
		return out
	}
	out.Body, out.Encoding = base64.StdEncoding.EncodeToString(body), api.OutboundAIContentEncodingBase64
	return out
}

// trimPartialRune drops an incomplete UTF-8 sequence at the end of body.
func trimPartialRune(body []byte) []byte {
	for cut := 1; cut <= utf8.UTFMax-1 && cut <= len(body); cut++ {
		if utf8.RuneStart(body[len(body)-cut]) {
			if !utf8.FullRune(body[len(body)-cut:]) {
				return body[:len(body)-cut]
			}
			return body
		}
	}
	return body
}
