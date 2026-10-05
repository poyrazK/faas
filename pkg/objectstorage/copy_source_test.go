package objectstorage

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 538
func TestMultipartCopyRangeBounds(t *testing.T) {
	for _, tc := range []struct {
		raw          string
		source, want int64
		invalid      bool
	}{
		{"", 1, 1, false},
		{"bytes=0-0", api.MinMultipartPartBytes + 1, 1, false},
		{"bytes=1-5242880", api.MinMultipartPartBytes + 1, api.MinMultipartPartBytes, false},
		{"bytes=0-0", api.MinMultipartPartBytes, 0, true},
		{"bytes=0-5242881", api.MinMultipartPartBytes + 1, 0, true},
		{"bytes=-10", 10000000, 0, true}, {"bytes=1-", 10000000, 0, true},
		{"bytes=2-1", 10000000, 0, true}, {"bytes=0-1,3-4", 10000000, 0, true},
		{"bytes=+0-1", 10000000, 0, true}, {"bytes=0- 1", 10000000, 0, true},
		{"bytes=0-9223372036854775807", api.MaxObjectUploadBytes, 0, true},
		{"", api.MaxObjectSinglePutBytes + 1, 0, true},
		{"bytes=0-9", api.MaxObjectUploadBytes, 10, false},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			r, err := ParseCopySourceRange(tc.raw)
			var size int64
			if err == nil {
				size, err = MultipartCopySize(CopySourceSnapshot{SizeBytes: tc.source}, r)
			}
			if errors.Is(err, ErrInvalid) != tc.invalid || size != tc.want {
				t.Fatal(size, err)
			}
			if !tc.invalid && r != nil && r.String() != tc.raw {
				t.Fatal(r.String())
			}
		})
	}
}

func TestCopySourceETagConditions(t *testing.T) {
	for _, tc := range []struct {
		match, none string
		want        error
	}{
		{"", "", nil}, {`"source"`, "", nil}, {"*", `"other"`, nil},
		{`"other"`, "", ErrPreconditionFailed}, {"", `"source"`, ErrPreconditionFailed},
		{"", "*", ErrPreconditionFailed}, {`"source"`, `"source"`, ErrPreconditionFailed},
		{`W/"source"`, "", ErrInvalid}, {"source", "", ErrInvalid},
		{`"source", "other"`, "", ErrInvalid}, {"\"bad\n\"", "", ErrInvalid},
	} {
		t.Run(tc.match+tc.none, func(t *testing.T) {
			if err := (CopySourceConditions{IfMatch: tc.match, IfNoneMatch: tc.none}).Check(CopySourceSnapshot{ETag: `"source"`}); !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
		})
	}
}
