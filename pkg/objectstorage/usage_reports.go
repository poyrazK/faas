package objectstorage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

// ImportUsageReports consumes atomic, operator-owned exports. The S3 data API
// cannot report billing traffic; each provider adapter exports this common
// cumulative format from its authoritative usage source. Never substitute
// presign counts, inventory estimates, or synthesized zeroes for absent data.
func (r *Registry) ImportUsageReports(ctx context.Context, accept func(api.ObjectStorageUsageReport) error) error {
	var failed bool
	for backend, path := range r.usageReportPaths {
		if err := ctx.Err(); err != nil {
			return err
		}
		reports, err := readUsageReports(path)
		if err != nil {
			failed = true
			continue
		}
		for _, report := range reports {
			if err := ctx.Err(); err != nil {
				return err
			}
			if report.BackendID != backend {
				failed = true
				continue
			}
			if _, err := r.Resolve(backend, report.BackendFingerprint); err != nil {
				failed = true
				continue
			}
			if err := accept(report); err != nil {
				failed = true
			}
		}
	}
	if failed {
		return errors.New("object storage: provider usage import failed")
	}
	return nil
}

func readUsageReports(path string) ([]api.ObjectStorageUsageReport, error) {
	f, err := os.Open(path) //nolint:forbidigo // Operator-owned export path from trusted backend configuration, never customer input.
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4<<20 {
		return nil, ErrInvalid
	}
	dec := json.NewDecoder(io.LimitReader(f, (4<<20)+1))
	dec.DisallowUnknownFields()
	var reports []api.ObjectStorageUsageReport
	if err := dec.Decode(&reports); err != nil {
		return nil, ErrInvalid
	}
	var extra any
	if dec.Decode(&extra) != io.EOF || len(reports) > 10000 {
		return nil, ErrInvalid
	}
	return reports, nil
}
