package objectstorage

import (
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// CopySourceConditions are source predicates, distinct from destination
// PUT/completion conditions. Each ETag header accepts one strong ETag or '*'.
type CopySourceConditions struct {
	IfMatch, IfNoneMatch               string
	IfModifiedSince, IfUnmodifiedSince *time.Time
}

func (c CopySourceConditions) HasDates() bool {
	return c.IfModifiedSince != nil || c.IfUnmodifiedSince != nil
}

func (c CopySourceConditions) Empty() bool {
	return c.IfMatch == "" && c.IfNoneMatch == "" && !c.HasDates()
}

func (c CopySourceConditions) Valid() bool {
	return validCopyETagCondition(c.IfMatch) && validCopyETagCondition(c.IfNoneMatch) && validCopyDate(c.IfModifiedSince) && validCopyDate(c.IfUnmodifiedSince)
}

func validCopyDate(t *time.Time) bool {
	return t == nil || !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999 && t.Nanosecond() == 0
}

func validCopyETagCondition(value string) bool {
	if value == "" || value == "*" {
		return true
	}
	if len(value) < 3 || len(value) > api.MaxObjectWriteETagBytes || value[0] != '"' || value[len(value)-1] != '"' {
		return false
	}
	for _, b := range []byte(value[1 : len(value)-1]) {
		if b < 33 || b == 127 || b == '"' {
			return false
		}
	}
	return true
}

// Check preflights ETag predicates and verifies that source identity can
// preserve date semantics. The provider evaluates dates during the copy.
func (c CopySourceConditions) Check(source CopySourceSnapshot) error {
	if !c.Valid() {
		return ErrInvalid
	}
	if c.IfMatch != "" && c.IfMatch != "*" && c.IfMatch != source.ETag || c.IfNoneMatch == "*" || c.IfNoneMatch != "" && c.IfNoneMatch == source.ETag {
		return ErrPreconditionFailed
	}
	// Only the customer's If-Match suppresses If-Unmodified-Since. The
	// internal ETag fence must never change customer predicate precedence.
	effectiveDates := c.IfModifiedSince != nil || c.IfUnmodifiedSince != nil && c.IfMatch == ""
	if effectiveDates {
		if source.ProviderVersionID == "" || source.ProviderVersionID == "null" {
			return ErrUnsupported
		}
		if !validNativeVersionID(source.ProviderVersionID) {
			return ErrUnavailable
		}
		// The provider evaluates dates on this immutable version, with the
		// customer's exact predicates and its native precedence rules.
	}
	return nil
}

// CopySourceRange is a closed byte interval. Open-ended and suffix ranges are
// not part of UploadPartCopy's wire contract.
type CopySourceRange struct{ First, Last int64 }

func ParseCopySourceRange(value string) (*CopySourceRange, error) {
	if value == "" {
		return nil, nil
	}
	if !strings.HasPrefix(value, "bytes=") {
		return nil, ErrInvalid
	}
	first, last, ok := strings.Cut(strings.TrimPrefix(value, "bytes="), "-")
	if !ok || first == "" || last == "" || strings.Trim(first+last, "0123456789") != "" {
		return nil, ErrInvalid
	}
	a, err := strconv.ParseInt(first, 10, 64)
	if err != nil {
		return nil, ErrInvalid
	}
	b, err := strconv.ParseInt(last, 10, 64)
	if err != nil || a > b || b >= api.MaxObjectUploadBytes {
		return nil, ErrInvalid
	}
	return &CopySourceRange{First: a, Last: b}, nil
}

func (r *CopySourceRange) String() string {
	if r == nil {
		return ""
	}
	return "bytes=" + strconv.FormatInt(r.First, 10) + "-" + strconv.FormatInt(r.Last, 10)
}

// MultipartCopySize validates the actual copied bytes, not the whole source.
// Only the last completed part may be smaller than the multipart minimum;
// that ordering rule is enforced by completion, as for uploaded parts.
func MultipartCopySize(source CopySourceSnapshot, r *CopySourceRange) (int64, error) {
	if source.SizeBytes < 1 || source.SizeBytes > api.MaxObjectUploadBytes {
		return 0, ErrInvalid
	}
	size := source.SizeBytes
	if r != nil {
		if source.SizeBytes <= api.MinMultipartPartBytes || r.First < 0 || r.Last < r.First || r.Last >= source.SizeBytes {
			return 0, ErrInvalid
		}
		size = r.Last - r.First + 1
	}
	if size > api.MaxObjectSinglePutBytes {
		return 0, ErrInvalid
	}
	return size, nil
}
