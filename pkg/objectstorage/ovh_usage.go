package objectstorage

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var (
	// ErrOVHRequestMetricsMissing is returned instead of publishing an
	// incomplete report. The OVH current-usage API exposes storage,
	// bandwidth, and cost by bucket, but not a request counter.
	ErrOVHRequestMetricsMissing = errors.New("object storage: ovh request metrics are unavailable")
	ErrOVHUsageInvalid          = errors.New("object storage: invalid ovh usage response")
)

const (
	ovhDefaultAPIBase       = "https://api.ovh.com/1.0"
	ovhMaxResponseBytes     = 8 << 20
	ovhMaxHistoryEntries    = 2000
	ovhGiBBytes             = int64(1 << 30)
	ovhMicrocentsPerMilcent = int64(1000)
)

// OVHUsageBucket is a durable Gregale-to-provider attribution binding. The
// physical name comes from the object bucket catalog, never from customer
// input in the usage response.
type OVHUsageBucket struct {
	AccountID    string
	PhysicalName string
}

// OVHRequestMetric is an authoritative cumulative request count for one
// physical bucket and UTC period. OVH does not currently return this value in
// its public-cloud usage API, so deployments must provide it from a qualified
// provider metric source before reports can be published.
type OVHRequestMetric struct {
	PhysicalName string
	Count        int64
}

// OVHBucketUsage contains the provider quantities normalized to Gregale's
// units. StoredByteHours are bytes multiplied by hours; EgressBytes includes
// public and internal outgoing bandwidth reported by OVH.
type OVHBucketUsage struct {
	StoredByteHours int64
	EgressBytes     int64
	CostMillicents  int64
}

// OVHUsageAPI is the narrow provider seam used by OVHUsageExporter. Keeping
// it small makes the adapter easy to test and leaves room for another OVH API
// implementation without changing report attribution.
type OVHUsageAPI interface {
	UsageForPeriod(context.Context, time.Time, time.Time) (map[string]OVHBucketUsage, error)
}

// OVHBucketCatalog and OVHRequestMetricsSource are supplied by the operator's
// deployment integration. A catalog implementation normally reads the
// durable Gregale object-bucket rows. Request metrics must be cumulative for
// the requested UTC month; missing or partial metrics fail closed.
type OVHBucketCatalog func(context.Context, string, string) ([]OVHUsageBucket, error)
type OVHRequestMetricsSource func(context.Context, string, time.Time, time.Time) ([]OVHRequestMetric, error)

// OVHUsageExporter maps OVH bucket usage into normalized Gregale reports.
// It deliberately does not estimate request counts or costs from S3 traffic,
// signed URLs, inventory, or a customer rate card.
type OVHUsageExporter struct {
	API            OVHUsageAPI
	Catalog        OVHBucketCatalog
	RequestMetrics OVHRequestMetricsSource
	Source         string
}

func (e OVHUsageExporter) ExportUsageReports(ctx context.Context, req UsageReportExportRequest) ([]api.ObjectStorageUsageReport, error) {
	if e.API == nil || e.Catalog == nil {
		return nil, ErrConfiguration
	}
	if e.RequestMetrics == nil {
		return nil, ErrOVHRequestMetricsMissing
	}
	if err := validateUsageReportExportRequest(req); err != nil {
		return nil, err
	}
	source := e.Source
	if source == "" {
		source = "ovh-public-cloud"
	}
	if len(source) > maxUsageReportSource {
		return nil, ErrInvalid
	}
	buckets, err := e.Catalog(ctx, req.BackendID, req.BackendFingerprint)
	if err != nil {
		return nil, err
	}
	bucketByName := make(map[string]OVHUsageBucket, len(buckets))
	for _, bucket := range buckets {
		if bucket.AccountID == "" || bucket.PhysicalName == "" {
			return nil, ErrInvalid
		}
		if _, exists := bucketByName[bucket.PhysicalName]; exists {
			return nil, ErrConflict
		}
		bucketByName[bucket.PhysicalName] = bucket
	}
	usage, err := e.API.UsageForPeriod(ctx, req.PeriodStart, req.ObservedAt)
	if err != nil {
		return nil, err
	}
	for physicalName := range usage {
		if _, ok := bucketByName[physicalName]; !ok {
			return nil, fmt.Errorf("%w: unknown physical bucket", ErrOVHUsageInvalid)
		}
	}
	metrics, err := e.RequestMetrics(ctx, req.BackendID, req.PeriodStart, req.ObservedAt)
	if err != nil {
		return nil, err
	}
	requestsByBucket := make(map[string]int64, len(metrics))
	for _, metric := range metrics {
		if metric.PhysicalName == "" || metric.Count < 0 {
			return nil, ErrInvalid
		}
		if _, ok := bucketByName[metric.PhysicalName]; !ok {
			return nil, fmt.Errorf("%w: unknown request-metric bucket", ErrOVHUsageInvalid)
		}
		if _, exists := requestsByBucket[metric.PhysicalName]; exists {
			return nil, ErrConflict
		}
		requestsByBucket[metric.PhysicalName] = metric.Count
	}
	if len(requestsByBucket) != len(bucketByName) {
		return nil, ErrOVHRequestMetricsMissing
	}

	names := make([]string, 0, len(bucketByName))
	for physicalName := range bucketByName {
		names = append(names, physicalName)
	}
	sort.Strings(names)
	reports := make([]api.ObjectStorageUsageReport, 0, len(names))
	for _, physicalName := range names {
		bucket := bucketByName[physicalName]
		bucketUsage := usage[physicalName]
		reports = append(reports, api.ObjectStorageUsageReport{
			AccountID:          bucket.AccountID,
			BackendID:          req.BackendID,
			BackendFingerprint: req.BackendFingerprint,
			Source:             source,
			PeriodStart:        req.PeriodStart,
			ObservedAt:         req.ObservedAt,
			StoredByteHours:    bucketUsage.StoredByteHours,
			RequestCount:       requestsByBucket[physicalName],
			EgressBytes:        bucketUsage.EgressBytes,
			CostMillicents:     bucketUsage.CostMillicents,
		})
	}
	return coalesceUsageReports(reports)
}

// coalesceUsageReports combines multiple provider buckets belonging to one
// Gregale account. A customer can own more than one logical bucket, while the
// normalized import contract requires one row per account.
func coalesceUsageReports(reports []api.ObjectStorageUsageReport) ([]api.ObjectStorageUsageReport, error) {
	byAccount := make(map[string]api.ObjectStorageUsageReport, len(reports))
	for _, report := range reports {
		old, ok := byAccount[report.AccountID]
		if !ok {
			byAccount[report.AccountID] = report
			continue
		}
		var err error
		old.StoredByteHours, err = addUsageValue(old.StoredByteHours, report.StoredByteHours)
		if err != nil {
			return nil, ErrInvalid
		}
		old.RequestCount, err = addUsageValue(old.RequestCount, report.RequestCount)
		if err != nil {
			return nil, ErrInvalid
		}
		old.EgressBytes, err = addUsageValue(old.EgressBytes, report.EgressBytes)
		if err != nil {
			return nil, ErrInvalid
		}
		old.CostMillicents, err = addUsageValue(old.CostMillicents, report.CostMillicents)
		if err != nil {
			return nil, ErrInvalid
		}
		byAccount[report.AccountID] = old
	}
	accounts := make([]string, 0, len(byAccount))
	for accountID := range byAccount {
		accounts = append(accounts, accountID)
	}
	sort.Strings(accounts)
	out := make([]api.ObjectStorageUsageReport, 0, len(accounts))
	for _, accountID := range accounts {
		out = append(out, byAccount[accountID])
	}
	return out, nil
}

func addUsageValue(left, right int64) (int64, error) {
	if left < 0 || right < 0 || right > math.MaxInt64-left {
		return 0, ErrInvalid
	}
	return left + right, nil
}

// OVHUsageClient reads the Public Cloud usage API using OVH's signed API
// headers. It only performs GET requests and never logs or returns response
// bodies, which may contain provider-specific identifiers.
type OVHUsageClient struct {
	BaseURL           string
	ServiceName       string
	ApplicationKey    string
	ApplicationSecret string
	ConsumerKey       string
	HTTPClient        *http.Client
	Timestamp         func(context.Context) (int64, error)
}

func (c *OVHUsageClient) UsageForPeriod(ctx context.Context, from, to time.Time) (map[string]OVHBucketUsage, error) {
	if c == nil || c.ServiceName == "" || c.ApplicationKey == "" || c.ApplicationSecret == "" || c.ConsumerKey == "" {
		return nil, ErrConfiguration
	}
	if !validOVHPathPart(c.ServiceName) {
		return nil, ErrConfiguration
	}
	if from.IsZero() || to.IsZero() || !from.Equal(utcMonth(from)) || to.Before(from) || to.After(time.Now().UTC().Add(time.Minute)) {
		return nil, ErrInvalid
	}
	history, err := c.history(ctx, from, to)
	if err != nil {
		return nil, err
	}
	usage := make(map[string]OVHBucketUsage)
	for _, entry := range history {
		if !validOVHPathPart(entry.ID) {
			return nil, ErrOVHUsageInvalid
		}
		detail, err := c.historyDetail(ctx, entry.ID)
		if err != nil {
			return nil, err
		}
		if detail.HourlyUsage == nil {
			continue
		}
		for _, storage := range detail.HourlyUsage.Storage {
			if storage.BucketName == "" {
				return nil, ErrOVHUsageInvalid
			}
			row, err := normalizeOVHStorage(storage)
			if err != nil {
				return nil, err
			}
			old := usage[storage.BucketName]
			old.StoredByteHours, err = addUsageValue(old.StoredByteHours, row.StoredByteHours)
			if err != nil {
				return nil, ErrInvalid
			}
			old.EgressBytes, err = addUsageValue(old.EgressBytes, row.EgressBytes)
			if err != nil {
				return nil, ErrInvalid
			}
			old.CostMillicents, err = addUsageValue(old.CostMillicents, row.CostMillicents)
			if err != nil {
				return nil, ErrInvalid
			}
			usage[storage.BucketName] = old
		}
	}
	return usage, nil
}

func normalizeOVHStorage(storage ovhHourlyStorage) (OVHBucketUsage, error) {
	var out OVHBucketUsage
	if storage.Stored != nil {
		stored, err := quantityToBytes(storage.Stored.Quantity, "GiBh")
		if err != nil {
			return OVHBucketUsage{}, err
		}
		out.StoredByteHours = stored
	}
	for _, bandwidth := range []*ovhBandwidth{storage.OutgoingBandwidth, storage.OutgoingInternalBandwidth} {
		if bandwidth == nil {
			continue
		}
		bytes, err := quantityToBytes(bandwidth.Quantity, "GiB")
		if err != nil {
			return OVHBucketUsage{}, err
		}
		out.EgressBytes, err = addUsageValue(out.EgressBytes, bytes)
		if err != nil {
			return OVHBucketUsage{}, err
		}
	}
	var err error
	out.CostMillicents, err = priceToMillicents(storage.TotalPrice)
	if err != nil {
		return OVHBucketUsage{}, err
	}
	return out, nil
}

func quantityToBytes(quantity *ovhQuantity, expectedUnit string) (int64, error) {
	if quantity == nil || quantity.Unit != expectedUnit || math.IsNaN(quantity.Value) || math.IsInf(quantity.Value, 0) || quantity.Value < 0 {
		return 0, ErrOVHUsageInvalid
	}
	value := quantity.Value * float64(ovhGiBBytes)
	if value > float64(math.MaxInt64) || math.Trunc(value) != value {
		return 0, ErrOVHUsageInvalid
	}
	return int64(value), nil
}

func priceToMillicents(price ovhPrice) (int64, error) {
	if price.CurrencyCode != "EUR" || price.PriceInUcents == nil || *price.PriceInUcents < 0 {
		return 0, ErrOVHUsageInvalid
	}
	value := *price.PriceInUcents
	if value > math.MaxInt64-(ovhMicrocentsPerMilcent-1) {
		return 0, ErrOVHUsageInvalid
	}
	return (value + ovhMicrocentsPerMilcent - 1) / ovhMicrocentsPerMilcent, nil
}

type ovhUsageHistory struct {
	ID string `json:"id"`
}

type ovhUsageHistoryDetail struct {
	HourlyUsage *ovhHourlyResources `json:"hourlyUsage"`
}

type ovhHourlyResources struct {
	Storage []ovhHourlyStorage `json:"storage"`
}

type ovhHourlyStorage struct {
	BucketName                string        `json:"bucketName"`
	OutgoingBandwidth         *ovhBandwidth `json:"outgoingBandwidth"`
	OutgoingInternalBandwidth *ovhBandwidth `json:"outgoingInternalBandwidth"`
	Stored                    *ovhStored    `json:"stored"`
	TotalPrice                ovhPrice      `json:"totalPrice"`
}

type ovhStored struct {
	Quantity *ovhQuantity `json:"quantity"`
}

type ovhBandwidth struct {
	Quantity *ovhQuantity `json:"quantity"`
}

type ovhQuantity struct {
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

type ovhPrice struct {
	CurrencyCode  string `json:"currencyCode"`
	PriceInUcents *int64 `json:"priceInUcents"`
}

func (p *ovhPrice) UnmarshalJSON(data []byte) error {
	var value struct {
		CurrencyCode  string `json:"currencyCode"`
		PriceInUcents *int64 `json:"priceInUcents"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	p.CurrencyCode = value.CurrencyCode
	p.PriceInUcents = value.PriceInUcents
	return nil
}

func (c *OVHUsageClient) history(ctx context.Context, from, to time.Time) ([]ovhUsageHistory, error) {
	query := url.Values{}
	query.Set("from", from.UTC().Format(time.RFC3339))
	query.Set("to", to.UTC().Format(time.RFC3339))
	var history []ovhUsageHistory
	if err := c.getJSON(ctx, "/cloud/project/"+c.ServiceName+"/usage/history", query, &history); err != nil {
		return nil, err
	}
	if len(history) > ovhMaxHistoryEntries {
		return nil, ErrOVHUsageInvalid
	}
	return history, nil
}

func (c *OVHUsageClient) historyDetail(ctx context.Context, id string) (ovhUsageHistoryDetail, error) {
	var detail ovhUsageHistoryDetail
	err := c.getJSON(ctx, "/cloud/project/"+c.ServiceName+"/usage/history/"+id, nil, &detail)
	return detail, err
}

func validOVHPathPart(value string) bool {
	if value == "" || value == "." || value == ".." || strings.ContainsAny(value, "/?#") {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func (c *OVHUsageClient) getJSON(ctx context.Context, requestPath string, query url.Values, dst any) error {
	baseURL := c.BaseURL
	if baseURL == "" {
		baseURL = ovhDefaultAPIBase
	}
	base, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil {
		return ErrConfiguration
	}
	base.Path = path.Join(base.Path, requestPath)
	base.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return ErrInvalid
	}
	timestamp, err := c.apiTimestamp(ctx)
	if err != nil {
		return err
	}
	date := strconv.FormatInt(timestamp, 10)
	body := ""
	signatureInput := strings.Join([]string{c.ApplicationSecret, c.ConsumerKey, date, http.MethodGet, base.String(), body}, "+")
	hash := ovhSHA1([]byte(signatureInput))
	req.Header.Set("X-Ovh-Application", c.ApplicationKey)
	req.Header.Set("X-Ovh-Consumer", c.ConsumerKey)
	req.Header.Set("X-Ovh-Timestamp", date)
	req.Header.Set("X-Ovh-Signature", "$1$"+hex.EncodeToString(hash[:]))
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, err := client.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("object storage: ovh usage request failed with status %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, ovhMaxResponseBytes+1))
	if err := decoder.Decode(dst); err != nil {
		return ErrOVHUsageInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return ErrOVHUsageInvalid
	}
	return nil
}

// ovhSHA1 is the wire-compatible digest required by OVH's legacy API
// signature protocol. It is intentionally kept private and must not be used
// for password hashing, integrity protection, or any new authentication.
func ovhSHA1(data []byte) [20]byte {
	const blockSize = 64
	length := len(data)
	padding := blockSize - ((length + 9) % blockSize)
	padded := make([]byte, length+9+padding)
	copy(padded, data)
	padded[length] = 0x80
	binary.BigEndian.PutUint64(padded[len(padded)-8:], uint64(length)*8)

	h0 := uint32(0x67452301)
	h1 := uint32(0xefcdab89)
	h2 := uint32(0x98badcfe)
	h3 := uint32(0x10325476)
	h4 := uint32(0xc3d2e1f0)
	for offset := 0; offset < len(padded); offset += blockSize {
		var words [80]uint32
		for i := 0; i < 16; i++ {
			words[i] = binary.BigEndian.Uint32(padded[offset+i*4:])
		}
		for i := 16; i < len(words); i++ {
			words[i] = bits.RotateLeft32(words[i-3]^words[i-8]^words[i-14]^words[i-16], 1)
		}
		a, b, c, d, e := h0, h1, h2, h3, h4
		for i, word := range words {
			var function, constant uint32
			switch {
			case i < 20:
				function = (b & c) | (^b & d)
				constant = 0x5a827999
			case i < 40:
				function = b ^ c ^ d
				constant = 0x6ed9eba1
			case i < 60:
				function = (b & c) | (b & d) | (c & d)
				constant = 0x8f1bbcdc
			default:
				function = b ^ c ^ d
				constant = 0xca62c1d6
			}
			temp := bits.RotateLeft32(a, 5) + function + e + constant + word
			e, d, c, b, a = d, c, bits.RotateLeft32(b, 30), a, temp
		}
		h0 += a
		h1 += b
		h2 += c
		h3 += d
		h4 += e
	}
	var digest [20]byte
	binary.BigEndian.PutUint32(digest[0:4], h0)
	binary.BigEndian.PutUint32(digest[4:8], h1)
	binary.BigEndian.PutUint32(digest[8:12], h2)
	binary.BigEndian.PutUint32(digest[12:16], h3)
	binary.BigEndian.PutUint32(digest[16:20], h4)
	return digest
}

func (c *OVHUsageClient) apiTimestamp(ctx context.Context) (int64, error) {
	if c.Timestamp != nil {
		return c.Timestamp(ctx)
	}
	baseURL := c.BaseURL
	if baseURL == "" {
		baseURL = ovhDefaultAPIBase
	}
	base, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil {
		return 0, ErrConfiguration
	}
	base.Path = path.Join(base.Path, "/auth/time")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return 0, ErrInvalid
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, ErrUnavailable
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return 0, fmt.Errorf("object storage: ovh timestamp request failed with status %d", resp.StatusCode)
	}
	var raw json.Number
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 128))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return 0, ErrOVHUsageInvalid
	}
	timestamp, err := raw.Int64()
	if err != nil {
		return 0, ErrOVHUsageInvalid
	}
	return timestamp, nil
}
