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
	// OpHead reads the current object's size and version, not its body.
	OpHead Op = "head"
	// OpGetGeneration reads exactly one generation (GCS).
	OpGetGeneration Op = "get_generation"
	// OpCreate writes only when the key holds nothing.
	OpCreate Op = "create"
	// OpReplace writes only when the key holds exactly one version: a GCS
	// generation, or an S3 ETag.
	OpReplace Op = "replace"
	// OpCreateMultipart starts an S3 multipart upload; content type and
	// metadata are signed here.
	OpCreateMultipart Op = "create_multipart"
	// OpUploadPart uploads one part of an S3 multipart upload.
	OpUploadPart Op = "upload_part"
	// OpCompleteMultipart completes an S3 multipart upload, create-only
	// without an ETag or replacing exactly that ETag.
	OpCompleteMultipart Op = "complete_multipart"
	// OpAbortMultipart abandons an S3 multipart upload.
	OpAbortMultipart Op = "abort_multipart"
)

// Writes reports whether op writes.
func (o Op) Writes() bool {
	switch o {
	case OpCreate, OpReplace, OpCreateMultipart, OpUploadPart, OpCompleteMultipart, OpAbortMultipart:
		return true
	}
	return false
}

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
	// Generation conditions OpGetGeneration and a GCS OpReplace.
	Generation int64
	// ETag conditions an S3 OpReplace or OpCompleteMultipart.
	ETag string
	// UploadID and PartNumber name an S3 multipart upload and its part.
	UploadID   string
	PartNumber int32
	// ContentType and Metadata are signed into a write that creates the
	// object (OpCreate, OpReplace, OpCreateMultipart), so the node cannot
	// change them.
	ContentType string
	Metadata    map[string]string
}

// Validate checks the request's shape, not its authorization.
func (r Request) Validate() error {
	upload := r.UploadID != ""
	switch r.Op {
	case OpGet, OpHead, OpCreate, OpCreateMultipart:
		if r.Generation != 0 || r.ETag != "" || upload || r.PartNumber != 0 {
			return fmt.Errorf("storagegrant: %s takes no condition or upload", r.Op)
		}
	case OpGetGeneration:
		if r.Generation <= 0 || r.ETag != "" || upload || r.PartNumber != 0 {
			return fmt.Errorf("storagegrant: %s needs a positive generation only", r.Op)
		}
	case OpReplace:
		if (r.Generation > 0) == (r.ETag != "") || r.Generation < 0 || upload || r.PartNumber != 0 {
			return fmt.Errorf("storagegrant: %s needs exactly one of a generation or an ETag", r.Op)
		}
	case OpUploadPart:
		if !upload || r.PartNumber < 1 || r.PartNumber > 10000 || r.Generation != 0 || r.ETag != "" {
			return fmt.Errorf("storagegrant: %s needs an upload and a part number from 1 to 10000", r.Op)
		}
	case OpCompleteMultipart:
		if !upload || r.PartNumber != 0 || r.Generation != 0 {
			return fmt.Errorf("storagegrant: %s needs an upload", r.Op)
		}
	case OpAbortMultipart:
		if !upload || r.PartNumber != 0 || r.Generation != 0 || r.ETag != "" {
			return fmt.Errorf("storagegrant: %s needs an upload only", r.Op)
		}
	default:
		return fmt.Errorf("storagegrant: unknown operation %q", r.Op)
	}
	if upload && (len(r.UploadID) > 1024 || strings.ContainsAny(r.UploadID, "\r\n")) {
		return errors.New("storagegrant: upload id is not signable")
	}
	if strings.ContainsAny(r.ETag, "\r\n") || len(r.ETag) > 256 {
		return errors.New("storagegrant: ETag is not signable")
	}
	describes := r.Op == OpCreate || r.Op == OpReplace || r.Op == OpCreateMultipart
	if !describes && (r.ContentType != "" || len(r.Metadata) > 0) {
		return fmt.Errorf("storagegrant: %s takes no content type or metadata", r.Op)
	}
	for k, v := range r.Metadata {
		if !plainSegment(k) || strings.ContainsAny(v, "\r\n") || len(v) > 1024 {
			return fmt.Errorf("storagegrant: metadata %q is not signable", k)
		}
	}
	if strings.ContainsAny(r.ContentType, "\r\n") || len(r.ContentType) > 255 {
		return errors.New("storagegrant: content type is not signable")
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
	switch op {
	case OpHead:
		return http.MethodHead
	case OpCreateMultipart, OpCompleteMultipart:
		return http.MethodPost
	case OpAbortMultipart:
		return http.MethodDelete
	}
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
