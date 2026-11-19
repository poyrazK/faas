package storagegrant

// adr: 20261117190000

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Op is what a grant lets the node do with one object.
type Op string

const (
	// OpGet reads the current object.
	OpGet Op = "get"
	// OpGetGeneration reads exactly one generation.
	OpGetGeneration Op = "get_generation"
	// OpCreate writes only when the key holds nothing.
	OpCreate Op = "create"
	// OpReplace writes only when the key holds exactly one generation.
	OpReplace Op = "replace"
)

// Writes reports whether op writes.
func (o Op) Writes() bool { return o == OpCreate || o == OpReplace }

// TTL is how long a grant stays valid.
const TTL = 5 * time.Minute

// MaxKeysPerRequest bounds one GrantStorageObjects call.
const MaxKeysPerRequest = 64

// ErrUnsupported is returned for an operation the store cannot condition.
var ErrUnsupported = errors.New("storagegrant: operation unsupported by this store")

// Request is one object operation to sign.
type Request struct {
	Key string
	Op  Op
	// Generation conditions OpGetGeneration and OpReplace; it must be
	// positive for them and zero otherwise.
	Generation int64
	// ContentType and Metadata are signed into a write, so the node cannot
	// change them.
	ContentType string
	Metadata    map[string]string
}

// Validate checks the request's shape, not its authorization.
func (r Request) Validate() error {
	switch r.Op {
	case OpGet, OpCreate:
		if r.Generation != 0 {
			return fmt.Errorf("storagegrant: %s takes no generation", r.Op)
		}
	case OpGetGeneration, OpReplace:
		if r.Generation <= 0 {
			return fmt.Errorf("storagegrant: %s needs a positive generation", r.Op)
		}
	default:
		return fmt.Errorf("storagegrant: unknown operation %q", r.Op)
	}
	if !r.Op.Writes() && (r.ContentType != "" || len(r.Metadata) > 0) {
		return fmt.Errorf("storagegrant: a read takes no content type or metadata")
	}
	for k, v := range r.Metadata {
		if !plainSegment(k) || strings.ContainsAny(v, "\r\n") || len(v) > 1024 {
			return fmt.Errorf("storagegrant: metadata %q is not signable", k)
		}
	}
	if Resolve(r.Key).Class == ClassUnknown {
		return fmt.Errorf("storagegrant: key %q is outside the granted keyspace", r.Key)
	}
	return nil
}

// Grant is a signed request the node sends as is.
type Grant struct {
	Method string
	URL    string
	// Headers must be sent exactly; they are part of the signature.
	Headers map[string]string
	Expires time.Time
}

// Signer signs object requests with the control plane's store credentials.
type Signer interface {
	Sign(ctx context.Context, r Request, expires time.Time) (Grant, error)
}

func method(op Op) string {
	if op.Writes() {
		return http.MethodPut
	}
	return http.MethodGet
}

// sortedHeaderList renders headers as "name:value" in a stable order.
func sortedHeaderList(h map[string]string) []string {
	out := make([]string, 0, len(h))
	for k, v := range h {
		out = append(out, k+":"+v)
	}
	sort.Strings(out)
	return out
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
