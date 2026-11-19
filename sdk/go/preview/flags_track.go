package preview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Conversion events for experiments (ADR 20261115163113).

// flagSDKCapabilities is what this SDK advertises to the runtime endpoints.
const flagSDKCapabilities = "subject-targeting-v1, attribute-targeting-v1, kill-v1, track-v1, track-v2, cohort-targeting-v1"

const (
	flagEventBatch         = 100
	flagEventQueueMax      = 1000
	flagEventFlushInterval = 5 * time.Second
	flagEventMaxAbsValue   = int64(1_000_000_000_000)
	// Product analytics bounds (ADR 20261117084505, pkg/api/limits.go).
	flagEventMaxProperties       = 10
	flagEventPropertyValueMaxLen = 256
	flagEventMaxFlags            = 32
)

var (
	flagEventName        = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
	flagEventPropertyKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

type flagEvent struct {
	Name       string            `json:"name"`
	CustomerID string            `json:"customer_id"`
	Value      int64             `json:"value,omitempty"`
	At         string            `json:"at"`
	Subject    string            `json:"subject,omitempty"`
	Properties map[string]string `json:"properties,omitempty"`
	Flags      map[string]string `json:"flags,omitempty"`
}

// TrackOptions are the optional parts of an event for TrackWith.
type TrackOptions struct {
	// Value is an integer, such as cents.
	Value int64
	// Properties are up to 10 string properties: keys match
	// ^[a-z][a-z0-9_]{0,63}$, values are at most 256 bytes.
	Properties map[string]string
	// Subject overrides the subject set with WithSubject. Only its hash is stored.
	Subject string
}

type flagEventQueue struct {
	mu      sync.Mutex
	events  []flagEvent
	dropped int64
	sendMu  sync.Mutex
}

func (q *flagEventQueue) drop(n int) {
	q.mu.Lock()
	q.dropped += int64(n)
	q.mu.Unlock()
}

// Track records a conversion event for the verified customer of the request
// in ctx, for experiments. value is an integer, such as cents. It returns
// false, and counts the event in DroppedEvents, when ctx carries no request
// with a verified customer, the name or value is invalid, or the local queue
// is full. Events are sent in batches every 5 s; Close or Flush sends the rest.
func (f *GregaleFlags) Track(ctx context.Context, name string, value int64) bool {
	return f.TrackWith(ctx, name, TrackOptions{Value: value})
}

// TrackWith is Track with properties and a subject (ADR 20261117084505). The
// event also carries every flag decision the request made before the call, so
// any event can be split by flag variant. It returns false, and counts the
// event in DroppedEvents, when Track would or when the properties or subject
// are invalid.
func (f *GregaleFlags) TrackWith(ctx context.Context, name string, opts TrackOptions) bool {
	state, err := f.requestState(ctx)
	value := opts.Value
	if err != nil || state.customer == "" || !flagEventName.MatchString(name) || value > flagEventMaxAbsValue || value < -flagEventMaxAbsValue || !validFlagEventProperties(opts.Properties) {
		f.events.drop(1)
		return false
	}
	subject := opts.Subject
	if subject == "" {
		subject, _ = ctx.Value(flagSubjectContextKey{}).(string)
	}
	if subject != "" && !validFlagSubjectID(subject) {
		f.events.drop(1)
		return false
	}
	event := flagEvent{Name: name, CustomerID: state.customer, Value: value, At: f.now().UTC().Format(time.RFC3339Nano), Subject: subject, Flags: state.servedFlags()}
	if len(opts.Properties) > 0 {
		event.Properties = maps.Clone(opts.Properties)
	}
	q := &f.events
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.events) >= flagEventQueueMax {
		q.dropped++
		return false
	}
	q.events = append(q.events, event)
	return true
}

func validFlagEventProperties(properties map[string]string) bool {
	if len(properties) > flagEventMaxProperties {
		return false
	}
	for k, v := range properties {
		if !flagEventPropertyKey.MatchString(k) || len(v) > flagEventPropertyValueMaxLen || !utf8.ValidString(v) {
			return false
		}
	}
	return true
}

// servedFlags is the served value of each flag the request evaluated, as a
// string, at most 32 in key order.
func (s *flagRequestState) servedFlags() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.evidence) == 0 {
		return nil
	}
	keys := slices.Sorted(maps.Keys(s.evidence))
	if len(keys) > flagEventMaxFlags {
		keys = keys[:flagEventMaxFlags]
	}
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		switch v := s.evidence[key].Value.(type) {
		case bool:
			out[key] = strconv.FormatBool(v)
		case string:
			out[key] = v
		default:
			out[key] = fmt.Sprint(v)
		}
	}
	return out
}

// DroppedEvents is the number of events dropped locally or refused by the API
// since the client was created.
func (f *GregaleFlags) DroppedEvents() int64 {
	f.events.mu.Lock()
	defer f.events.mu.Unlock()
	return f.events.dropped
}

// Flush sends queued events now, at most 100 per call. A failed batch is
// dropped and counted, never retried.
func (f *GregaleFlags) Flush(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	q := &f.events
	q.sendMu.Lock()
	defer q.sendMu.Unlock()
	var firstErr error
	for {
		q.mu.Lock()
		n := min(len(q.events), flagEventBatch)
		batch := append([]flagEvent(nil), q.events[:n]...)
		q.events = q.events[n:]
		q.mu.Unlock()
		if len(batch) == 0 {
			return firstErr
		}
		accepted, err := f.sendEvents(ctx, batch)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if accepted < len(batch) {
			q.drop(len(batch) - accepted)
		}
	}
}

func (f *GregaleFlags) sendEvents(parent context.Context, batch []flagEvent) (int, error) {
	ctx, cancel := context.WithTimeout(parent, f.timeout)
	defer cancel()
	token, err := f.identityToken(ctx)
	if err != nil {
		return 0, err
	}
	body, err := json.Marshal(map[string]any{"events": batch})
	if err != nil {
		return 0, fmt.Errorf("faas: encode Flags events: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, f.eventsURL.String(), bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("faas: create Flags events request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Faas-Flags-Capabilities", flagSDKCapabilities)
	response, err := f.httpClient.Do(request)
	if err != nil {
		return 0, fmt.Errorf("faas: send Flags events: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return 0, fmt.Errorf("faas: Flags events refused (%s)", response.Status)
	}
	raw, err := readBounded(response.Body, 16<<10)
	if err != nil {
		return 0, fmt.Errorf("faas: read Flags events response: %w", err)
	}
	var result struct {
		Accepted *int `json:"accepted"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.Accepted == nil {
		return len(batch), nil
	}
	return min(*result.Accepted, len(batch)), nil
}

func (f *GregaleFlags) identityToken(ctx context.Context) (string, error) {
	identity, err := f.getJSON(ctx, f.identityURL.String(), maxFlagIdentityBytes, nil)
	if err != nil {
		return "", fmt.Errorf("faas: read workload identity: %w", err)
	}
	var credentials struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(identity, &credentials); err != nil || credentials.AccessToken == "" || len(credentials.AccessToken) > maxFlagIdentityTokenSize || strings.ContainsAny(credentials.AccessToken, "\r\n \t") {
		return "", errors.New("faas: invalid Flags workload identity")
	}
	return credentials.AccessToken, nil
}
