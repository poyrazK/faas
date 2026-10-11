package outbound

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// aiRequest is the replayable body of an AI POST (ADR-983). It lets the
// gateway retry, cache by exact body, fall back across route targets, and
// adjust OpenAI stream options without re-reading the caller's stream.
type aiRequest struct {
	body []byte
	// streaming reports a JSON body with "stream": true.
	streaming bool
	// usageInjected reports that the gateway turned on
	// stream_options.include_usage, so the extra usage chunk must be hidden
	// from the caller.
	usageInjected bool
}

// prefixedReadCloser replays bytes already read before the rest of src.
type prefixedReadCloser struct {
	io.Reader
	src io.Closer
}

func (p prefixedReadCloser) Close() error { return p.src.Close() }

// readAIRequest buffers an AI POST body of at most
// api.OutboundAIRequestBufferMaxBytes. A larger body is restored onto r and
// streams as before, so the result is nil and every phase 2 feature is off.
func readAIRequest(r *http.Request, integration Integration, path string) (*aiRequest, error) {
	if integration.AIProvider == "" || r.Method != http.MethodPost || r.Body == nil || r.Body == http.NoBody ||
		r.ContentLength > api.OutboundAIRequestBufferMaxBytes {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, api.OutboundAIRequestBufferMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > api.OutboundAIRequestBufferMaxBytes {
		r.Body = prefixedReadCloser{Reader: io.MultiReader(bytes.NewReader(body), r.Body), src: r.Body}
		return nil, nil
	}
	req := &aiRequest{body: body}
	if !jsonRequest(r.Header.Get("Content-Type"), body) {
		return req, nil
	}
	var envelope struct {
		Stream        bool `json:"stream"`
		StreamOptions *struct {
			IncludeUsage *bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return req, nil
	}
	req.streaming = envelope.Stream
	callerAskedForUsage := envelope.StreamOptions != nil && envelope.StreamOptions.IncludeUsage != nil && *envelope.StreamOptions.IncludeUsage
	if integration.AIProvider == api.OutboundAIProviderOpenAI && envelope.Stream && strings.HasSuffix(path, "/completions") && !callerAskedForUsage {
		if rewritten, ok := withIncludeUsage(body); ok {
			req.body = rewritten
			req.usageInjected = true
		}
	}
	return req, nil
}

func jsonRequest(contentType string, body []byte) bool {
	if mediaType, _, err := mime.ParseMediaType(contentType); err == nil {
		return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
	}
	trimmed := bytes.TrimSpace(body)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

// withIncludeUsage sets stream_options.include_usage on a JSON object body,
// keeping every other field's raw value.
func withIncludeUsage(body []byte) ([]byte, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return nil, false
	}
	options := map[string]json.RawMessage{}
	if raw, ok := fields["stream_options"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if json.Unmarshal(raw, &options) != nil || options == nil {
			return nil, false
		}
	}
	options["include_usage"] = json.RawMessage("true")
	encodedOptions, err := json.Marshal(options)
	if err != nil {
		return nil, false
	}
	fields["stream_options"] = encodedOptions
	rewritten, err := json.Marshal(fields)
	if err != nil {
		return nil, false
	}
	return rewritten, true
}

// withModel replaces the JSON "model" field for a route target override.
func withModel(body []byte, model string) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return nil, errors.New("outbound route: model override needs a JSON object body")
	}
	encoded, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	fields["model"] = encoded
	return json.Marshal(fields)
}

// setReplayableBody gives the upstream request a fresh reader per attempt.
func setReplayableBody(req *http.Request, body []byte) {
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	if len(body) == 0 {
		req.Body = http.NoBody
	}
}

// aiCacheEligible extends the opt-in response cache (ADR-294) to buffered,
// non-streaming JSON POST calls of an AI integration.
func aiCacheEligible(req *http.Request, ai *aiRequest, ttlSeconds int) bool {
	if ai == nil || ai.streaming || ttlSeconds <= 0 || req == nil || req.Method != http.MethodPost {
		return false
	}
	return jsonRequest(req.Header.Get("Content-Type"), ai.body) && !requestCacheControlDisablesCaching(req.Header)
}
