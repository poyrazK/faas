package profiling

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/pprof/profile"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"google.golang.org/protobuf/encoding/protowire"
)

type Backend interface {
	Push(context.Context, Principal, *profile.Profile) error
	Query(context.Context, string, string, string, api.ProfileQuery) (*profile.Profile, error)
}

type Pyroscope struct {
	base   string
	token  string
	client *http.Client
}

func NewPyroscope(base, token string) (*Pyroscope, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid Pyroscope endpoint")
	}
	return &Pyroscope{base: strings.TrimSuffix(base, "/"), token: token, client: &http.Client{Timeout: api.ProfileQueryTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (b *Pyroscope) request(ctx context.Context, path, tenant, contentType string, body []byte) (*http.Response, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, b.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create profile request: %w", err)
	}
	r.Header.Set("Content-Type", contentType)
	r.Header.Set("X-Scope-OrgID", tenant)
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	resp, err := b.client.Do(r)
	if err != nil {
		return nil, fmt.Errorf("profile backend unavailable")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("profile backend returned HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

func (b *Pyroscope) Push(ctx context.Context, principal Principal, p *profile.Profile) error {
	p = p.Copy()
	reasons := sanitizeRouteSamples(p, principal.Routes)
	if trusted, ok := ctx.Value(routeReasonsKey{}).([]string); ok && len(trusted) == len(reasons) {
		reasons = append([]string(nil), trusted...)
	}
	if err := normalizeCPU(p, true); err != nil {
		return err
	}
	encodeRouteFrames(p, reasons)
	var encoded bytes.Buffer
	if err := p.WriteUncompressed(&encoded); err != nil {
		return fmt.Errorf("encode CPU profile: %w", err)
	}
	hash := sha256.Sum256(encoded.Bytes())
	identity := sampleIdentity{
		ID:         uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("%s:%s:%x", principal.InstanceID, principal.Generation, hash))).String(),
		Collector:  uuid.NewSHA1(uuid.NameSpaceOID, []byte(principal.InstanceID+":"+principal.Generation)).String(),
		ReceivedAt: time.Now(),
	}
	if supplied, ok := ctx.Value(rawSampleIdentityKey{}).(sampleIdentity); ok {
		identity = supplied
	}
	series, err := encodeSeries(principal, identity, "process_cpu", p)
	if err != nil {
		return err
	}
	metadataID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(identity.ID+":coverage")).String()
	metadataIdentity := identity
	metadataIdentity.ID = metadataID
	metadata, err := encodeSeries(principal, metadataIdentity, "gregale_profile_coverage", receivedRouteProfile(p, identity, reasons, routeRequestReport(ctx)))
	if err != nil {
		return err
	}
	body := protoBytes(nil, 1, series)
	body = protoBytes(body, 1, metadata)
	return b.pushSeries(ctx, principal.AccountID, body)
}

func encodeSeries(principal Principal, identity sampleIdentity, name string, p *profile.Profile) ([]byte, error) {
	var encoded bytes.Buffer
	if err := p.WriteUncompressed(&encoded); err != nil {
		return nil, fmt.Errorf("encode profile: %w", err)
	}
	// Pyroscope query deduplication uses series identity and capture time.
	// Separate VM/process lifetimes need distinct series even when their
	// CPU samples and timestamps coincide. No guest label is forwarded.
	labels := [][2]string{{"__name__", name}, {"service_name", "gregale"}, {"app_id", principal.AppID}, {"deployment_id", principal.DeploymentID}, {"scope", principal.Scope}, {"runtime", principal.Runtime}, {"collector_id", identity.Collector}}
	var series []byte
	for _, label := range labels {
		var pair []byte
		pair = protoBytes(pair, 1, []byte(label[0]))
		pair = protoBytes(pair, 2, []byte(label[1]))
		series = protoBytes(series, 1, pair)
	}
	sample := protoBytes(nil, 1, encoded.Bytes())
	sample = protoBytes(sample, 2, []byte(identity.ID))
	series = protoBytes(series, 2, sample)
	return series, nil
}

func (b *Pyroscope) pushSeries(ctx context.Context, tenant string, body []byte) error {
	resp, err := b.request(ctx, "/push.v1.PusherService/Push", tenant, "application/proto", body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, api.ProfileControlMaxBytes))
	return err
}

func protoBytes(body []byte, field protowire.Number, value []byte) []byte {
	body = protowire.AppendTag(body, field, protowire.BytesType)
	return protowire.AppendBytes(body, value)
}

// The Connect unary endpoint takes a protobuf SelectMergeProfileRequest and
// returns a raw google.v1.Profile (pprof). No profiling backend credentials or
// tenant selectors are exposed to dashboard callers.
func (b *Pyroscope) Query(ctx context.Context, tenant, appID, scope string, q api.ProfileQuery) (*profile.Profile, error) {
	return b.queryProfile(ctx, tenant, appID, scope, q, CPUProfileType, api.ProfileMaxViewNodes)
}

func (b *Pyroscope) queryProfile(ctx context.Context, tenant, appID, scope string, q api.ProfileQuery, profileType string, maxNodes int) (*profile.Profile, error) {
	selector := fmt.Sprintf("{app_id=%s,deployment_id=%s,scope=%s}", strconv.Quote(appID), strconv.Quote(q.DeploymentID), strconv.Quote(scope))
	if q.Runtime != "" {
		selector = strings.TrimSuffix(selector, "}") + ",runtime=" + strconv.Quote(q.Runtime) + "}"
	}
	var body []byte
	for i, s := range []string{profileType, selector} {
		body = protowire.AppendTag(body, protowire.Number(i+1), protowire.BytesType)
		body = protowire.AppendString(body, s)
	}
	for i, t := range []int64{q.Start.UnixMilli(), q.End.UnixMilli()} {
		body = protowire.AppendTag(body, protowire.Number(i+3), protowire.VarintType)
		body = protowire.AppendVarint(body, uint64(t))
	}
	body = protowire.AppendTag(body, 5, protowire.VarintType)
	body = protowire.AppendVarint(body, uint64(maxNodes))
	resp, err := b.request(ctx, "/querier.v1.QuerierService/SelectMergeProfile", tenant, "application/proto", body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, api.ProfileMaxCompressedBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read merged profile: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	p, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	if len(p.Sample) == 0 {
		return nil, nil
	}
	return p, nil
}

// Ready checks backend health without querying tenant data.
func (b *Pyroscope) Ready(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.base+"/ready", nil)
	if err != nil {
		return false
	}
	if b.token != "" {
		req.Header.Set("Authorization", "Bearer "+b.token)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func routeRequestReport(ctx context.Context) *routeRequestMetadata {
	report, _ := ctx.Value(routeRequestReportKey{}).(*routeRequestMetadata)
	return report
}
