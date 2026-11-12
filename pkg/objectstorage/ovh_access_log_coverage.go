package objectstorage

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// OVHAccessLogCoverage is operator-owned evidence from a qualified collector,
// not an inference from nonempty logs or their most recent timestamp. It binds
// complete month-to-date coverage to an immutable set of log object bytes,
// including explicit coverage of quiet buckets. Best-effort logs alone cannot
// produce this evidence. No exporter in this package manufactures it.
type OVHAccessLogCoverage struct {
	Version            int                  `json:"version"`
	BackendID          string               `json:"backend_id"`
	BackendFingerprint string               `json:"backend_fingerprint"`
	LogBucket          string               `json:"log_bucket"`
	LogPrefix          string               `json:"log_prefix"`
	PeriodStart        time.Time            `json:"period_start"`
	CoveredThrough     time.Time            `json:"covered_through"`
	Buckets            []string             `json:"buckets"`
	Objects            []OVHAccessLogObject `json:"objects"`
}

type OVHAccessLogObject struct {
	Key    string `json:"key"`
	SHA256 string `json:"sha256"`
}

type OVHAccessLogCoverageSource func(context.Context, time.Time) (OVHAccessLogCoverage, error)

func ovhAccessLogCoverageFile(path string) OVHAccessLogCoverageSource {
	return func(ctx context.Context, period time.Time) (OVHAccessLogCoverage, error) {
		if err := ctx.Err(); err != nil {
			return OVHAccessLogCoverage{}, err
		}
		if !validUsageReportPath(path) {
			return OVHAccessLogCoverage{}, ErrOVHRequestMetricsMissing
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
			return OVHAccessLogCoverage{}, ErrOVHRequestMetricsMissing
		}
		file, err := os.Open(path) //nolint:forbidigo // Operator-owned evidence path from trusted backend configuration, never customer input.
		if err != nil {
			return OVHAccessLogCoverage{}, ErrOVHRequestMetricsMissing
		}
		defer func() { _ = file.Close() }()
		data, err := io.ReadAll(io.LimitReader(file, api.MaxObjectStorageAccessLogCoverageBytes+1))
		if err != nil || len(data) > api.MaxObjectStorageAccessLogCoverageBytes {
			return OVHAccessLogCoverage{}, ErrOVHUsageInvalid
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		var rows []OVHAccessLogCoverage
		if info.Size() > api.MaxObjectStorageAccessLogCoverageBytes || decoder.Decode(&rows) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
			return OVHAccessLogCoverage{}, ErrOVHUsageInvalid
		}
		var result OVHAccessLogCoverage
		found := false
		for _, row := range rows {
			if row.PeriodStart.Equal(period) {
				if found {
					return OVHAccessLogCoverage{}, ErrOVHUsageInvalid
				}
				result = row
				found = true
			}
		}
		if !found {
			return result, ErrOVHRequestMetricsMissing
		}
		return result, ctx.Err()
	}
}

func (s OVHAccessLogRequestMetrics) coverage(ctx context.Context, period, observed time.Time, counts map[string]int64) (OVHAccessLogCoverage, error) {
	if s.Coverage == nil {
		return OVHAccessLogCoverage{}, ErrOVHRequestMetricsMissing
	}
	c, err := s.Coverage(ctx, period)
	if err != nil {
		return c, err
	}
	if c.Version != 1 || s.BackendID == "" || s.BackendFingerprint == "" || c.BackendID != s.BackendID || c.BackendFingerprint != s.BackendFingerprint || c.LogBucket != s.LogBucket || c.LogPrefix != s.LogPrefix || !c.PeriodStart.Equal(period) || !utcMonth(period).Equal(period) || c.CoveredThrough.Before(observed) || c.CoveredThrough.After(time.Now()) {
		return c, ErrOVHRequestMetricsMissing
	}
	if len(c.Buckets) != len(counts) || c.Objects == nil || len(c.Objects) > s.MaxObjects {
		return c, ErrOVHRequestMetricsMissing
	}
	seen := map[string]bool{}
	for _, bucket := range c.Buckets {
		if _, ok := counts[bucket]; !ok || seen[bucket] {
			return c, ErrOVHRequestMetricsMissing
		}
		seen[bucket] = true
	}
	seen = map[string]bool{}
	for _, object := range c.Objects {
		digest, err := hex.DecodeString(object.SHA256)
		if !ValidKey(object.Key) || !strings.HasPrefix(object.Key, s.LogPrefix) || seen[object.Key] || err != nil || len(digest) != 32 || hex.EncodeToString(digest) != object.SHA256 {
			return c, ErrOVHUsageInvalid
		}
		seen[object.Key] = true
	}
	return c, nil
}
