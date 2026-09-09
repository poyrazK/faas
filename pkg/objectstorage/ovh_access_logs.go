package objectstorage

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

const (
	maxOVHAccessLogObjects = 10000
	maxOVHAccessLogBytes   = 64 << 20
)

// AccessLogObjectStore is the operator-only capability required to read OVH
// server access logs. It is intentionally narrower than the customer-facing
// Provider interface and is never exposed through an API response.
type AccessLogObjectStore interface {
	ListObjects(context.Context, string, string, string, int32) (ObjectPage, error)
	ObjectReader
}

// OVHAccessLogRequestMetrics derives cumulative request counts from OVH S3
// server-access-log records. OVH emits these records asynchronously (normally
// about an hour after the request), so callers should export with a matching
// coverage lag. The source scans the configured log prefix and filters by the
// physical bucket catalog, preventing other operator buckets from entering a
// tenant report.
type OVHAccessLogRequestMetrics struct {
	Store      AccessLogObjectStore
	LogBucket  string
	LogPrefix  string
	MaxObjects int
}

func (s OVHAccessLogRequestMetrics) Metrics(ctx context.Context, periodStart, observedAt time.Time, catalog []OVHUsageBucket) ([]OVHRequestMetric, error) {
	if s.Store == nil || s.LogBucket == "" || periodStart.IsZero() || observedAt.IsZero() || observedAt.Before(periodStart) {
		return nil, ErrConfiguration
	}
	if s.MaxObjects <= 0 || s.MaxObjects > maxOVHAccessLogObjects {
		s.MaxObjects = maxOVHAccessLogObjects
	}
	counts := make(map[string]int64, len(catalog))
	for _, bucket := range catalog {
		if bucket.PhysicalName == "" || bucket.AccountID == "" {
			return nil, ErrInvalid
		}
		if _, exists := counts[bucket.PhysicalName]; exists {
			return nil, ErrConflict
		}
		counts[bucket.PhysicalName] = 0
	}

	cursor := ""
	seenObjects := 0
	for {
		page, err := s.Store.ListObjects(ctx, s.LogBucket, s.LogPrefix, cursor, 1000)
		if err != nil {
			return nil, err
		}
		for _, object := range page.Items {
			seenObjects++
			if seenObjects > s.MaxObjects {
				return nil, fmt.Errorf("%w: access-log object limit exceeded", ErrOVHUsageInvalid)
			}
			if err := s.scanObject(ctx, object.Key, periodStart, observedAt, counts); err != nil {
				return nil, err
			}
		}
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor {
			return nil, fmt.Errorf("%w: access-log cursor did not advance", ErrOVHUsageInvalid)
		}
		cursor = page.NextCursor
	}
	if len(counts) > 0 && seenObjects == 0 {
		// An empty log bucket is indistinguishable from disabled logging or a
		// permissions mistake. Never turn that absence into an authoritative
		// all-zero request report.
		return nil, ErrOVHRequestMetricsMissing
	}

	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	metrics := make([]OVHRequestMetric, 0, len(names))
	for _, name := range names {
		metrics = append(metrics, OVHRequestMetric{PhysicalName: name, Count: counts[name]})
	}
	return metrics, nil
}

func (s OVHAccessLogRequestMetrics) scanObject(ctx context.Context, key string, periodStart, observedAt time.Time, counts map[string]int64) error {
	body, err := s.Store.ReadObject(ctx, s.LogBucket, key)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	reader := &countingReader{reader: io.LimitReader(body, maxOVHAccessLogBytes+1)}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 32<<10), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		bucket, at, ok := parseOVHAccessLogLine(line)
		if !ok {
			return fmt.Errorf("%w: malformed access-log record", ErrOVHUsageInvalid)
		}
		if at.Before(periodStart) || at.After(observedAt) {
			continue
		}
		if _, tracked := counts[bucket]; !tracked {
			continue
		}
		if counts[bucket] == int64(^uint64(0)>>1) {
			return ErrInvalid
		}
		counts[bucket]++
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if reader.n > maxOVHAccessLogBytes {
		return fmt.Errorf("%w: access-log object exceeds size limit", ErrOVHUsageInvalid)
	}
	return nil
}

type countingReader struct {
	reader io.Reader
	n      int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.n += int64(n)
	return n, err
}

// parseOVHAccessLogLine extracts the bucket and timestamp from OVH's access
// log format. Quoted request URI/user-agent fields and the bracketed timestamp
// are tokenized as single fields; the remaining fields are deliberately not
// interpreted so provider format additions do not change accounting.
func parseOVHAccessLogLine(line string) (string, time.Time, bool) {
	fields := splitOVHAccessLogFields(line)
	if len(fields) < 3 || fields[1] == "" {
		return "", time.Time{}, false
	}
	stamp := strings.Trim(fields[2], "[]")
	at, err := time.Parse("02/Jan/2006:15:04:05 -0700", stamp)
	if err != nil {
		return "", time.Time{}, false
	}
	return fields[1], at.UTC(), true
}

func splitOVHAccessLogFields(line string) []string {
	var fields []string
	for i := 0; i < len(line); {
		for i < len(line) && line[i] == ' ' {
			i++
		}
		if i == len(line) {
			break
		}
		start := i
		switch line[i] {
		case '[', '"':
			open := line[i]
			close := byte(']')
			if open == '"' {
				close = '"'
			}
			i++
			for i < len(line) {
				if line[i] == '\\' && open == '"' && i+1 < len(line) {
					i += 2
					continue
				}
				if line[i] == close {
					i++
					break
				}
				i++
			}
		default:
			for i < len(line) && line[i] != ' ' {
				i++
			}
		}
		fields = append(fields, line[start:i])
	}
	return fields
}
