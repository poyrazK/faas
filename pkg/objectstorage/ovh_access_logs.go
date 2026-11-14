package objectstorage

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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
// coverage lag. Only a qualified coverage manifest authorizes a complete report;
// the selected objects are digest-verified and filtered by bucket and period.
type OVHAccessLogRequestMetrics struct {
	Store                         AccessLogObjectStore
	LogBucket                     string
	LogPrefix                     string
	MaxObjects                    int
	MaxRecords                    int
	BackendID, BackendFingerprint string
	Coverage                      OVHAccessLogCoverageSource
}

func (s OVHAccessLogRequestMetrics) Metrics(ctx context.Context, periodStart, observedAt time.Time, catalog []OVHUsageBucket) ([]OVHRequestMetric, error) {
	if s.Store == nil || s.LogBucket == "" || periodStart.IsZero() || observedAt.IsZero() || observedAt.Before(periodStart) {
		return nil, ErrConfiguration
	}
	if s.MaxObjects <= 0 || s.MaxObjects > api.MaxObjectStorageAccessLogObjects {
		s.MaxObjects = api.MaxObjectStorageAccessLogObjects
	}
	if s.MaxRecords <= 0 || s.MaxRecords > api.MaxObjectStorageAccessLogRecords {
		s.MaxRecords = api.MaxObjectStorageAccessLogRecords
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
	if len(counts) == 0 {
		return []OVHRequestMetric{}, ctx.Err()
	}
	coverage, err := s.coverage(ctx, periodStart, observedAt, counts)
	if err != nil {
		return nil, err
	}
	seen := map[[32]byte][32]byte{}
	for _, object := range coverage.Objects {
		if err := s.scanObject(ctx, object, periodStart, observedAt, counts, seen); err != nil {
			return nil, err
		}
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

func (s OVHAccessLogRequestMetrics) scanObject(ctx context.Context, object OVHAccessLogObject, periodStart, observedAt time.Time, counts map[string]int64, seen map[[32]byte][32]byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	body, err := s.Store.ReadObject(ctx, s.LogBucket, object.Key)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	digest := sha256.New()
	reader := &countingReader{reader: io.TeeReader(io.LimitReader(body, api.MaxObjectStorageAccessLogBytes+1), digest)}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 32<<10), api.MaxObjectStorageAccessLogLineBytes)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		record, ok := parseOVHAccessLogLine(line)
		if !ok {
			return fmt.Errorf("%w: malformed access-log record", ErrOVHUsageInvalid)
		}
		if record.at.Before(periodStart) || record.at.After(observedAt) {
			continue
		}
		if _, tracked := counts[record.bucket]; !tracked {
			continue
		}
		identity := sha256.Sum256([]byte(record.bucket + "\x00" + record.id))
		if previous, exists := seen[identity]; exists {
			if previous != record.digest {
				return fmt.Errorf("%w: conflicting access-log request identity", ErrOVHUsageInvalid)
			}
			continue
		}
		if len(seen) >= s.MaxRecords {
			return fmt.Errorf("%w: access-log record limit exceeded", ErrOVHUsageInvalid)
		}
		seen[identity] = record.digest
		if counts[record.bucket] == int64(^uint64(0)>>1) {
			return ErrInvalid
		}
		counts[record.bucket]++
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if reader.n > api.MaxObjectStorageAccessLogBytes || hex.EncodeToString(digest.Sum(nil)) != object.SHA256 {
		return fmt.Errorf("%w: access-log object does not match coverage evidence", ErrOVHUsageInvalid)
	}
	return ctx.Err()
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

type ovhAccessLogRecord struct {
	bucket, id string
	at         time.Time
	digest     [32]byte
}

// A provider request ID identifies one request across log delivery retries.
// Compare the remaining fields too: conflicting records are not a known count.
func parseOVHAccessLogLine(line string) (ovhAccessLogRecord, bool) {
	fields := splitOVHAccessLogFields(line)
	if len(fields) < 10 || fields[1] == "" || fields[5] == "" || fields[5] == "-" || strings.ContainsAny(fields[5], "\x00\r\n\t ") {
		return ovhAccessLogRecord{}, false
	}
	at, err := time.Parse("02/Jan/2006:15:04:05 -0700", strings.Trim(fields[2], "[]"))
	if err != nil {
		return ovhAccessLogRecord{}, false
	}
	fields[2] = at.UTC().Format(time.RFC3339Nano)
	return ovhAccessLogRecord{bucket: fields[1], id: fields[5], at: at.UTC(), digest: sha256.Sum256([]byte(strings.Join(fields, "\x00")))}, true
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
