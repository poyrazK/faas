package logarchive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// DayKey is the whole-day object of one instance's archived UTC day,
// faas-logs/{instance}/{YYYY}/{MM}/{YYYY-MM-DD}.jsonl.gz. Before part objects
// (ADR-20261028110000) the shipper re-uploaded it whole on every flush.
func DayKey(instance, day string) string {
	if len(day) < 10 {
		// Spool days are YYYY-MM-DD; anything shorter comes from a buggy
		// caller and simply names an object that never exists.
		return fmt.Sprintf("faas-logs/%s/%s%s", instance, day, archiveGzipSuffix)
	}
	return fmt.Sprintf("faas-logs/%s/%s/%s/%s%s", instance, day[:4], day[5:7], day[:10], archiveGzipSuffix)
}

// DayPartPrefix holds the day's part objects, one per flush, named so that
// key order is write order: faas-logs/{instance}/{YYYY}/{MM}/{YYYY-MM-DD}/.
func DayPartPrefix(instance, day string) string {
	return strings.TrimSuffix(DayKey(instance, day), archiveGzipSuffix) + "/"
}

// dayPartListPage bounds one listing of a day's parts. A day of five-minute
// flushes is 288 parts.
const dayPartListPage = 1000

// OpenDay streams one instance's archived day as one gzip JSONL stream: the
// whole-day object, if the day has one, then each part object in key order.
// Each object is a complete gzip member, so the concatenation decodes as one
// stream. A day with neither answers the same *Permanent 404 as a missing
// object. A part that disappears between listing and reading (lifecycle
// expiry) is skipped.
func (c *S3Client) OpenDay(ctx context.Context, instance, day string) (io.ReadCloser, error) {
	var parts []string
	prefix := DayPartPrefix(instance, day)
	for marker := ""; ; {
		keys, next, err := c.ListObjectKeys(ctx, prefix, marker, dayPartListPage)
		if err != nil {
			return nil, fmt.Errorf("logarchive: list day parts: %w", err)
		}
		for _, key := range keys {
			if strings.HasSuffix(key, archiveGzipSuffix) {
				parts = append(parts, key)
			}
		}
		if next == "" {
			break
		}
		marker = next
	}
	whole, err := c.OpenObject(ctx, DayKey(instance, day))
	if err != nil && !isNotFound(err) {
		return nil, err
	}
	if whole == nil && len(parts) == 0 {
		return nil, &Permanent{StatusCode: http.StatusNotFound, Code: "NoSuchKey", Message: "no archived logs for " + DayKey(instance, day)}
	}
	return &dayReader{ctx: ctx, client: c, current: whole, parts: parts}, nil
}

func isNotFound(err error) bool {
	var permanent *Permanent
	return errors.As(err, &permanent) && permanent.StatusCode == http.StatusNotFound
}

// dayReader reads the open object, then opens the next part when it ends.
type dayReader struct {
	ctx     context.Context
	client  *S3Client
	current io.ReadCloser
	parts   []string
}

func (r *dayReader) Read(p []byte) (int, error) {
	for {
		if r.current == nil {
			if len(r.parts) == 0 {
				return 0, io.EOF
			}
			key := r.parts[0]
			r.parts = r.parts[1:]
			body, err := r.client.OpenObject(r.ctx, key)
			if isNotFound(err) {
				continue
			}
			if err != nil {
				return 0, err
			}
			r.current = body
		}
		n, err := r.current.Read(p)
		if errors.Is(err, io.EOF) {
			_ = r.current.Close()
			r.current = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (r *dayReader) Close() error {
	r.parts = nil
	if r.current == nil {
		return nil
	}
	err := r.current.Close()
	r.current = nil
	return err
}
