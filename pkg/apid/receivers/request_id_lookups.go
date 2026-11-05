// adr: 20261101085552
package receivers

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
)

// RequestIDLookupHub holds the lookup streams of the gateways that keep the
// request-ID journal on their node, and fans a lookup out to all of them. The
// private mTLS listener it is served on is the trust boundary, as for the
// other gateway telemetry services.
type RequestIDLookupHub struct {
	apidpb.UnimplementedRequestIDLookupsServer
	log *slog.Logger

	mu       sync.Mutex
	gateways map[*lookupGateway]struct{}
}

type lookupGateway struct {
	node   string
	stream apidpb.RequestIDLookups_ServeServer
	sendMu sync.Mutex

	mu      sync.Mutex
	waiting map[string]chan *apidpb.RequestIDLookupAnswer
	closed  bool
}

// NewRequestIDLookupHub returns an empty hub.
func NewRequestIDLookupHub(log *slog.Logger) *RequestIDLookupHub {
	if log == nil {
		log = slog.Default()
	}
	return &RequestIDLookupHub{log: log, gateways: make(map[*lookupGateway]struct{})}
}

// RegisterRequestIDLookups serves hub on s.
func RegisterRequestIDLookups(s *grpc.Server, hub *RequestIDLookupHub) {
	if hub != nil {
		apidpb.RegisterRequestIDLookupsServer(s, hub)
	}
}

// Serve registers a gateway once its first message names its node, then
// routes its answers to the lookups waiting for them until the stream ends.
func (h *RequestIDLookupHub) Serve(stream apidpb.RequestIDLookups_ServeServer) error {
	hello, err := stream.Recv()
	if err != nil {
		return err
	}
	if hello.GetNode() == "" {
		return status.Error(codes.InvalidArgument, "the first request-ID lookup message names the gateway's node")
	}
	gateway := &lookupGateway{node: hello.GetNode(), stream: stream, waiting: make(map[string]chan *apidpb.RequestIDLookupAnswer)}
	h.mu.Lock()
	h.gateways[gateway] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.gateways, gateway)
		h.mu.Unlock()
		gateway.close()
	}()
	for {
		answer, err := stream.Recv()
		if err != nil {
			return nil //nolint:nilerr // a gateway leaving ends its stream; the lookups it owed see a missing answer
		}
		gateway.deliver(answer)
	}
}

// Gateways reports how many gateways are serving lookups.
func (h *RequestIDLookupHub) Gateways() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.gateways)
}

// RequestIDLookupResult is the newest record any gateway found.
type RequestIDLookupResult struct {
	Found      bool
	TraceID    string
	ReceivedAt time.Time
	ExpiresAt  time.Time
	Node       string
	// Asked is how many gateways were asked; Incomplete is set when one of
	// them did not answer in time or could not read its journal, so a miss
	// may not be final.
	Asked      int
	Incomplete bool
}

// Lookup asks every serving gateway for the request ID, each within timeout,
// and returns the newest record found.
func (h *RequestIDLookupHub) Lookup(ctx context.Context, accountID, appID, requestID string, from, until time.Time, timeout time.Duration) RequestIDLookupResult {
	h.mu.Lock()
	gateways := make([]*lookupGateway, 0, len(h.gateways))
	for gateway := range h.gateways {
		gateways = append(gateways, gateway)
	}
	h.mu.Unlock()
	result := RequestIDLookupResult{Asked: len(gateways)}
	if len(gateways) == 0 {
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	answers := make(chan *apidpb.RequestIDLookupAnswer, len(gateways))
	for _, gateway := range gateways {
		go func() {
			answers <- gateway.ask(ctx, &apidpb.RequestIDLookupQuestion{
				QuestionId: uuid.NewString(), AccountId: accountID, AppId: appID, RequestId: requestID,
				ReceivedFromUnixMs: from.UnixMilli(), ReceivedUntilUnixMs: until.UnixMilli(),
			})
		}()
	}
	for range gateways {
		answer := <-answers
		switch {
		case answer == nil || answer.GetError() != "":
			result.Incomplete = true
		case answer.GetFound() && answer.GetRequestId() == requestID:
			received := time.UnixMilli(answer.GetReceivedAtUnixMs()).UTC()
			if !result.Found || received.After(result.ReceivedAt) {
				result.Found, result.TraceID, result.ReceivedAt, result.Node = true, answer.GetTraceId(), received, answer.GetNode()
				result.ExpiresAt = time.UnixMilli(answer.GetExpiresAtUnixMs()).UTC()
			}
		}
	}
	return result
}

// ask sends question and waits for its answer; nil means none came.
func (g *lookupGateway) ask(ctx context.Context, question *apidpb.RequestIDLookupQuestion) *apidpb.RequestIDLookupAnswer {
	reply := make(chan *apidpb.RequestIDLookupAnswer, 1)
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil
	}
	g.waiting[question.GetQuestionId()] = reply
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.waiting, question.GetQuestionId())
		g.mu.Unlock()
	}()
	g.sendMu.Lock()
	err := g.stream.Send(question)
	g.sendMu.Unlock()
	if err != nil {
		return nil
	}
	select {
	case answer := <-reply:
		return answer
	case <-ctx.Done():
		return nil
	}
}

func (g *lookupGateway) deliver(answer *apidpb.RequestIDLookupAnswer) {
	g.mu.Lock()
	reply := g.waiting[answer.GetQuestionId()]
	g.mu.Unlock()
	if reply != nil {
		select {
		case reply <- answer:
		default: // a duplicate answer; the first one already went
		}
	}
}

// close fails every lookup still waiting on this gateway.
func (g *lookupGateway) close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed = true
	for id, reply := range g.waiting {
		select {
		case reply <- nil:
		default:
		}
		delete(g.waiting, id)
	}
}
