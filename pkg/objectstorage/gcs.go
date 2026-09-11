package objectstorage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

const (
	gcsDefaultEndpoint = "https://storage.googleapis.com"
	gcsIAMSignEndpoint = "https://iamcredentials.googleapis.com"
	gcsManagedLabel    = "gregale-backend"
	gcsMaxControlBody  = 2 << 20
	gcsRequestTimeout  = 20 * time.Second
)

// GCS uses the native JSON API for bucket/object management, OAuth 2.0 for
// server-side XML multipart calls, and short-lived V4 signed URLs for customer
// data transfer. No downloaded service-account key or HMAC credential is used.
type GCS struct {
	store          gcsStore
	httpClient     *http.Client
	endpoint       *url.URL
	serviceAccount string
	location       string
	storageClass   string
	projectID      string
	managedLabel   string
	origins        []string
	sign           func(context.Context, []byte) ([]byte, error)
	now            func() time.Time
}

type gcsBucketSpec struct {
	ProjectID, Location, StorageClass, ManagedLabel string
	Origins                                         []string
}

type gcsBucketState struct {
	Location, ManagedLabel string
}

type gcsObjectState struct {
	Key          string
	ETag         string
	Size         int64
	LastModified time.Time
	Metadata     map[string]string
}

type gcsStore interface {
	CreateBucket(context.Context, string, gcsBucketSpec) error
	BucketState(context.Context, string) (gcsBucketState, error)
	ReconcileBucket(context.Context, string, gcsBucketSpec) error
	DeleteBucket(context.Context, string) error
	ListObjects(context.Context, string, string, string, string, int32) ([]gcsObjectState, []string, string, error)
	DeleteObject(context.Context, string, string) error
	ObjectState(context.Context, string, string) (gcsObjectState, error)
	CopyObject(context.Context, string, string, string, ObjectMetadata, string) (gcsObjectState, error)
}

type googleGCSStore struct {
	client *storage.Client
}

func NewGCS(c BackendConfig, _ func(string) string) (Provider, error) {
	ctx := context.Background()
	creds, err := google.FindDefaultCredentials(ctx, storage.ScopeFullControl)
	if err != nil {
		return nil, errors.New("GCS application default credentials are unavailable")
	}
	clientOptions := []option.ClientOption{option.WithCredentials(creds), storage.WithJSONReads(), storage.WithDisabledClientMetrics()}
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = gcsDefaultEndpoint
	} else {
		clientOptions = append(clientOptions, option.WithEndpoint(endpoint))
	}
	client, err := storage.NewClient(ctx, clientOptions...)
	if err != nil {
		return nil, errors.New("GCS client initialization failed")
	}
	oauthClient := oauth2.NewClient(context.Background(), creds.TokenSource)
	oauthClient.Timeout = gcsRequestTimeout
	oauthClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil {
		_ = client.Close()
		return nil, errors.New("GCS endpoint is invalid")
	}
	storageClass := c.GCSStorageClass
	if storageClass == "" {
		storageClass = "STANDARD"
	}
	p := &GCS{
		store:          &googleGCSStore{client: client},
		httpClient:     oauthClient,
		endpoint:       parsedEndpoint,
		serviceAccount: c.GCSServiceAccount,
		location:       c.GCSLocation,
		storageClass:   storageClass,
		projectID:      c.Namespace,
		managedLabel:   fingerprint(c)[:32],
		origins:        append([]string(nil), c.AllowedOrigins...),
		now:            func() time.Time { return time.Now().UTC() },
	}
	p.sign = (&gcsIAMBlobSigner{client: oauthClient, serviceAccount: c.GCSServiceAccount, endpoint: gcsIAMSignEndpoint}).Sign
	return p, nil
}

func (s *googleGCSStore) CreateBucket(ctx context.Context, bucket string, spec gcsBucketSpec) error {
	return s.client.Bucket(bucket).Create(ctx, spec.ProjectID, gcsCreateBucketAttrs(spec))
}

func (s *googleGCSStore) BucketState(ctx context.Context, bucket string) (gcsBucketState, error) {
	attrs, err := s.client.Bucket(bucket).Attrs(ctx)
	if err != nil {
		return gcsBucketState{}, err
	}
	return gcsBucketState{Location: attrs.Location, ManagedLabel: attrs.Labels[gcsManagedLabel]}, nil
}

func (s *googleGCSStore) ReconcileBucket(ctx context.Context, bucket string, spec gcsBucketSpec) error {
	update := storage.BucketAttrsToUpdate{
		CORS:                     gcsCORS(spec.Origins),
		PublicAccessPrevention:   storage.PublicAccessPreventionEnforced,
		SoftDeletePolicy:         &storage.SoftDeletePolicy{RetentionDuration: 0},
		StorageClass:             spec.StorageClass,
		UniformBucketLevelAccess: &storage.UniformBucketLevelAccess{Enabled: true},
	}
	update.SetLabel(gcsManagedLabel, spec.ManagedLabel)
	_, err := s.client.Bucket(bucket).Update(ctx, update)
	return err
}

func (s *googleGCSStore) DeleteBucket(ctx context.Context, bucket string) error {
	return s.client.Bucket(bucket).Delete(ctx)
}

func (s *googleGCSStore) ListObjects(ctx context.Context, bucket, prefix, delimiter, cursor string, limit int32) ([]gcsObjectState, []string, string, error) {
	iter := s.client.Bucket(bucket).Objects(ctx, &storage.Query{Prefix: prefix, Delimiter: delimiter, Projection: storage.ProjectionNoACL})
	pager := iterator.NewPager(iter, int(limit), cursor)
	var attrs []*storage.ObjectAttrs
	next, err := pager.NextPage(&attrs)
	if err != nil {
		return nil, nil, "", err
	}
	objects := make([]gcsObjectState, 0, len(attrs))
	prefixes := make([]string, 0)
	for _, attr := range attrs {
		if attr.Prefix != "" {
			prefixes = append(prefixes, attr.Prefix)
			continue
		}
		objects = append(objects, gcsObjectState{Key: attr.Name, ETag: attr.Etag, Size: attr.Size, LastModified: attr.Updated, Metadata: attr.Metadata})
	}
	return objects, prefixes, next, nil
}

func (s *googleGCSStore) DeleteObject(ctx context.Context, bucket, key string) error {
	return s.client.Bucket(bucket).Object(key).Delete(ctx)
}

func (s *googleGCSStore) ObjectState(ctx context.Context, bucket, key string) (gcsObjectState, error) {
	attr, err := s.client.Bucket(bucket).Object(key).Attrs(ctx)
	if err != nil {
		return gcsObjectState{}, err
	}
	return gcsObjectState{Key: attr.Name, ETag: attr.Etag, Size: attr.Size, LastModified: attr.Updated, Metadata: attr.Metadata}, nil
}

func (s *googleGCSStore) CopyObject(ctx context.Context, bucket, source, destination string, metadata ObjectMetadata, directive string) (gcsObjectState, error) {
	copier := s.client.Bucket(bucket).Object(destination).CopierFrom(s.client.Bucket(bucket).Object(source))
	if directive == "REPLACE" {
		copier.ObjectAttrs = storage.ObjectAttrs{
			CacheControl:       metadata.CacheControl,
			ContentDisposition: metadata.ContentDisposition,
			ContentEncoding:    metadata.ContentEncoding,
			ContentLanguage:    metadata.ContentLanguage,
			ContentType:        metadata.ContentType,
			Metadata:           metadata.Metadata,
		}
	}
	attrs, err := copier.Run(ctx)
	if err != nil {
		return gcsObjectState{}, err
	}
	if attrs == nil {
		return gcsObjectState{}, ErrUnavailable
	}
	return gcsObjectState{Key: attrs.Name, ETag: attrs.Etag, Size: attrs.Size, LastModified: attrs.Updated, Metadata: attrs.Metadata}, nil
}

func gcsCreateBucketAttrs(spec gcsBucketSpec) *storage.BucketAttrs {
	return &storage.BucketAttrs{
		Location:                 spec.Location,
		StorageClass:             spec.StorageClass,
		Labels:                   map[string]string{gcsManagedLabel: spec.ManagedLabel},
		UniformBucketLevelAccess: storage.UniformBucketLevelAccess{Enabled: true},
		PublicAccessPrevention:   storage.PublicAccessPreventionEnforced,
		SoftDeletePolicy:         &storage.SoftDeletePolicy{RetentionDuration: 0},
		CORS:                     gcsCORS(spec.Origins),
	}
}

func gcsCORS(origins []string) []storage.CORS {
	if len(origins) == 0 {
		return []storage.CORS{}
	}
	return []storage.CORS{{
		Origins: origins, Methods: []string{http.MethodGet, http.MethodHead, http.MethodPut},
		ResponseHeaders: []string{"Content-Type", "Content-Length", "Content-MD5", "ETag", "x-goog-hash"}, MaxAge: time.Hour,
	}}
}

func (p *GCS) bucketSpec() gcsBucketSpec {
	return gcsBucketSpec{ProjectID: p.projectID, Location: p.location, StorageClass: p.storageClass, ManagedLabel: p.managedLabel, Origins: p.origins}
}

func (p *GCS) CreateBucket(ctx context.Context, bucket string) error {
	spec := p.bucketSpec()
	err := p.store.CreateBucket(ctx, bucket, spec)
	if err == nil {
		return nil
	}
	if gcsStatusCode(err) != http.StatusConflict {
		return normalizeGCS(err)
	}
	state, inspectErr := p.store.BucketState(ctx, bucket)
	if inspectErr != nil {
		return normalizeGCS(inspectErr)
	}
	if !strings.EqualFold(state.Location, p.location) || state.ManagedLabel != p.managedLabel {
		return ErrConflict
	}
	return normalizeGCS(p.store.ReconcileBucket(ctx, bucket, spec))
}

func (p *GCS) DeleteBucket(ctx context.Context, bucket string) error {
	err := p.store.DeleteBucket(ctx, bucket)
	if errors.Is(normalizeGCS(err), ErrNotFound) {
		return nil
	}
	if gcsStatusCode(err) == http.StatusConflict {
		return ErrNotEmpty
	}
	return normalizeGCS(err)
}

func (p *GCS) ListObjects(ctx context.Context, bucket, prefix, cursor string, limit int32) (ObjectPage, error) {
	return p.ListObjectsDelimited(ctx, bucket, prefix, "", cursor, limit)
}

func (p *GCS) ListObjectsDelimited(ctx context.Context, bucket, prefix, delimiter, cursor string, limit int32) (ObjectPage, error) {
	if limit < 1 || limit > 1000 {
		return ObjectPage{}, ErrInvalid
	}
	objects, prefixes, next, err := p.store.ListObjects(ctx, bucket, prefix, delimiter, cursor, limit)
	if err != nil {
		return ObjectPage{}, normalizeGCS(err)
	}
	page := ObjectPage{Items: make([]Object, 0, len(objects)), CommonPrefixes: append([]string(nil), prefixes...), NextCursor: next}
	for _, object := range objects {
		if !ValidKey(object.Key) || object.Size < 0 {
			return ObjectPage{}, ErrUnavailable
		}
		page.Items = append(page.Items, Object{Key: object.Key, Size: object.Size, LastModified: object.LastModified})
	}
	return page, nil
}

func (p *GCS) DeleteObject(ctx context.Context, bucket, key string) error {
	if !ValidKey(key) {
		return ErrInvalid
	}
	err := p.store.DeleteObject(ctx, bucket, key)
	if errors.Is(normalizeGCS(err), ErrNotFound) {
		return nil
	}
	return normalizeGCS(err)
}

func (p *GCS) ObjectSize(ctx context.Context, bucket, key string) (int64, error) {
	if !ValidKey(key) {
		return 0, ErrInvalid
	}
	object, err := p.store.ObjectState(ctx, bucket, key)
	if err != nil {
		return 0, normalizeGCS(err)
	}
	if object.Size < 0 {
		return 0, ErrUnavailable
	}
	return object.Size, nil
}

func (p *GCS) CopyObject(ctx context.Context, bucket string, r CopyObjectRequest) (CopyObjectResult, error) {
	if !ValidKey(r.SourceKey) || !ValidKey(r.DestinationKey) {
		return CopyObjectResult{}, ErrInvalid
	}
	if r.MetadataDirective == "" {
		r.MetadataDirective = "COPY"
	}
	if r.MetadataDirective != "COPY" && r.MetadataDirective != "REPLACE" {
		return CopyObjectResult{}, ErrInvalid
	}
	if err := validateObjectMetadata(r.Metadata); err != nil {
		return CopyObjectResult{}, err
	}
	object, err := p.store.CopyObject(ctx, bucket, r.SourceKey, r.DestinationKey, r.Metadata, r.MetadataDirective)
	if err != nil {
		return CopyObjectResult{}, normalizeGCS(err)
	}
	if object.ETag == "" {
		return CopyObjectResult{}, ErrUnavailable
	}
	return CopyObjectResult{ETag: object.ETag, LastModified: object.LastModified}, nil
}

func (p *GCS) Presign(ctx context.Context, bucket string, r SignRequest) (SignedRequest, error) {
	if err := r.Validate(api.MaxObjectSinglePutBytes); err != nil {
		return SignedRequest{}, err
	}
	ttl := time.Duration(r.ExpiresIn) * time.Second
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	expiresAt := p.now().Add(ttl)
	opts := storage.SignedURLOptions{
		GoogleAccessID: p.serviceAccount, Method: r.Method, Expires: expiresAt, Scheme: storage.SigningSchemeV4,
		Style: storage.PathStyle(), QueryParameters: make(url.Values),
	}
	result := SignedRequest{Method: r.Method, Headers: map[string]string{}, ExpiresAt: expiresAt}
	switch r.Method {
	case http.MethodPut:
		contentType := r.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		opts.ContentType = contentType
		result.Headers["Content-Type"] = contentType
		if *r.SizeBytes == 0 {
			opts.MD5 = "1B2M2Y8AsgTpgAmY7PhCfg=="
			result.Headers["Content-MD5"] = opts.MD5
		} else {
			length := strconv.FormatInt(*r.SizeBytes, 10)
			opts.Headers = []string{"content-length:" + length}
			result.Headers["Content-Length"] = length
		}
	case http.MethodGet:
		opts.QueryParameters.Set("response-content-disposition", "attachment")
		opts.QueryParameters.Set("response-content-type", "application/octet-stream")
	}
	value, err := p.signedURL(ctx, bucket, r.Key, opts)
	if err != nil {
		return SignedRequest{}, err
	}
	result.URL = value
	return result, nil
}

func (p *GCS) signedURL(ctx context.Context, bucket, key string, opts storage.SignedURLOptions) (string, error) {
	opts.Hostname = p.endpoint.Host
	opts.Insecure = p.endpoint.Scheme == "http"
	opts.SignBytes = func(payload []byte) ([]byte, error) { return p.sign(ctx, payload) }
	value, err := storage.SignedURL(bucket, key, &opts)
	if err != nil {
		return "", normalizeGCSSigning(err)
	}
	return value, nil
}

func (p *GCS) EnsureMultipartUpload(ctx context.Context, bucket string, r MultipartCreateRequest) (string, error) {
	if r.SessionID == "" || len(r.SessionID) > 128 || !ValidKey(r.Key) || r.SizeBytes < 0 || r.SizeBytes > api.MaxObjectUploadBytes || ValidateContentType(r.ContentType) != nil {
		return "", ErrInvalid
	}
	var found, keyMarker, uploadMarker string
	for page := 0; page < 100; page++ {
		query := url.Values{"uploads": {""}, "prefix": {r.Key}, "max-uploads": {"1000"}}
		if keyMarker != "" {
			query.Set("key-marker", keyMarker)
		}
		if uploadMarker != "" {
			query.Set("upload-id-marker", uploadMarker)
		}
		var listed gcsListMultipartUploadsResult
		if err := p.xmlRequest(ctx, http.MethodGet, bucket, "", query, nil, nil, &listed); err != nil {
			return "", normalizeGCS(err)
		}
		for _, upload := range listed.Uploads {
			if upload.Key != r.Key {
				continue
			}
			if upload.UploadID == "" || found != "" && found != upload.UploadID {
				return "", ErrConflict
			}
			found = upload.UploadID
		}
		if !listed.IsTruncated {
			break
		}
		if listed.NextKeyMarker == "" || page == 99 {
			return "", ErrUnavailable
		}
		keyMarker, uploadMarker = listed.NextKeyMarker, listed.NextUploadIDMarker
	}
	if found != "" {
		return found, nil
	}
	contentType := r.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	headers := http.Header{"Content-Type": {contentType}, "x-goog-meta-" + multipartSessionMetadata: {r.SessionID}}
	var initiated gcsInitiateMultipartUploadResult
	if err := p.xmlRequest(ctx, http.MethodPost, bucket, r.Key, url.Values{"uploads": {""}}, headers, nil, &initiated); err != nil {
		return "", normalizeGCS(err)
	}
	if initiated.UploadID == "" {
		return "", ErrUnavailable
	}
	return initiated.UploadID, nil
}

func (p *GCS) PresignMultipartPart(ctx context.Context, bucket string, r MultipartPartRequest) (SignedRequest, error) {
	if !ValidKey(r.Key) || r.ProviderUploadID == "" || r.PartNumber < 1 || r.PartNumber > 10000 || r.SizeBytes < 1 || r.SizeBytes > api.MaxObjectSinglePutBytes || r.ExpiresIn < 0 || r.ExpiresIn > 900 {
		return SignedRequest{}, ErrInvalid
	}
	ttl := time.Duration(r.ExpiresIn) * time.Second
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	expiresAt := p.now().Add(ttl)
	length := strconv.FormatInt(r.SizeBytes, 10)
	opts := storage.SignedURLOptions{
		GoogleAccessID: p.serviceAccount, Method: http.MethodPut, Expires: expiresAt, Scheme: storage.SigningSchemeV4,
		Style: storage.PathStyle(), Headers: []string{"content-length:" + length},
		QueryParameters: url.Values{"partNumber": {strconv.FormatInt(int64(r.PartNumber), 10)}, "uploadId": {r.ProviderUploadID}},
	}
	value, err := p.signedURL(ctx, bucket, r.Key, opts)
	if err != nil {
		return SignedRequest{}, err
	}
	return SignedRequest{URL: value, Method: http.MethodPut, Headers: map[string]string{"Content-Length": length}, ExpiresAt: expiresAt}, nil
}

func (p *GCS) ListMultipartParts(ctx context.Context, bucket string, r MultipartListPartsRequest) (MultipartPartsPage, error) {
	if !ValidKey(r.Key) || r.ProviderUploadID == "" || r.PartNumberMarker < 0 || r.PartNumberMarker > 10000 || r.Limit < 1 || r.Limit > 1000 {
		return MultipartPartsPage{}, ErrInvalid
	}
	query := url.Values{
		"uploadId": {r.ProviderUploadID}, "part-number-marker": {strconv.FormatInt(int64(r.PartNumberMarker), 10)}, "max-parts": {strconv.FormatInt(int64(r.Limit), 10)},
	}
	var listed gcsListPartsResult
	if err := p.xmlRequest(ctx, http.MethodGet, bucket, r.Key, query, nil, nil, &listed); err != nil {
		return MultipartPartsPage{}, normalizeGCS(err)
	}
	page := MultipartPartsPage{Items: make([]MultipartPart, 0, len(listed.Parts))}
	for _, part := range listed.Parts {
		if part.PartNumber < 1 || part.PartNumber > 10000 || part.ETag == "" || len(part.ETag) > 256 || part.Size < 1 || part.Size > api.MaxObjectSinglePutBytes {
			return MultipartPartsPage{}, ErrUnavailable
		}
		page.Items = append(page.Items, MultipartPart{PartNumber: part.PartNumber, ETag: part.ETag, SizeBytes: part.Size, LastModified: part.LastModified})
	}
	if listed.IsTruncated {
		if listed.NextPartNumberMarker < 1 || listed.NextPartNumberMarker > 10000 || listed.NextPartNumberMarker <= r.PartNumberMarker {
			return MultipartPartsPage{}, ErrUnavailable
		}
		page.NextPartNumberMarker = listed.NextPartNumberMarker
	}
	return page, nil
}

func (p *GCS) CompleteMultipartUpload(ctx context.Context, bucket string, r MultipartCompleteRequest) error {
	if r.SessionID == "" || !ValidKey(r.Key) || r.ProviderUploadID == "" || r.SizeBytes <= 0 || len(r.Parts) < 1 || len(r.Parts) > 10000 {
		return ErrInvalid
	}
	body := gcsCompleteMultipartUpload{Parts: make([]gcsCompletedPart, 0, len(r.Parts))}
	var previousPart int32
	for _, part := range r.Parts {
		if part.PartNumber < 1 || part.PartNumber > 10000 || part.PartNumber <= previousPart || part.ETag == "" || len(part.ETag) > 256 {
			return ErrInvalid
		}
		previousPart = part.PartNumber
		body.Parts = append(body.Parts, gcsCompletedPart(part))
	}
	payload, err := xml.Marshal(body)
	if err != nil {
		return ErrUnavailable
	}
	err = p.xmlRequest(ctx, http.MethodPost, bucket, r.Key, url.Values{"uploadId": {r.ProviderUploadID}}, http.Header{"Content-Type": {"application/xml"}}, payload, nil)
	if err == nil {
		return nil
	}
	if !errors.Is(normalizeGCS(err), ErrNotFound) {
		return normalizeGCS(err)
	}
	object, attrErr := p.store.ObjectState(ctx, bucket, r.Key)
	if attrErr != nil {
		return normalizeGCS(attrErr)
	}
	if object.Size != r.SizeBytes || object.Metadata[multipartSessionMetadata] != r.SessionID {
		return ErrConflict
	}
	return nil
}

func (p *GCS) AbortMultipartUpload(ctx context.Context, bucket string, r MultipartAbortRequest) error {
	if !ValidKey(r.Key) || r.ProviderUploadID == "" {
		return ErrInvalid
	}
	err := p.xmlRequest(ctx, http.MethodDelete, bucket, r.Key, url.Values{"uploadId": {r.ProviderUploadID}}, nil, nil, nil)
	if errors.Is(normalizeGCS(err), ErrNotFound) {
		return nil
	}
	return normalizeGCS(err)
}

type gcsInitiateMultipartUploadResult struct {
	UploadID string `xml:"UploadId"`
}

type gcsMultipartUpload struct {
	Key      string `xml:"Key"`
	UploadID string `xml:"UploadId"`
}

type gcsListMultipartUploadsResult struct {
	IsTruncated        bool                 `xml:"IsTruncated"`
	NextKeyMarker      string               `xml:"NextKeyMarker"`
	NextUploadIDMarker string               `xml:"NextUploadIdMarker"`
	Uploads            []gcsMultipartUpload `xml:"Upload"`
}

type gcsMultipartPart struct {
	PartNumber   int32     `xml:"PartNumber"`
	ETag         string    `xml:"ETag"`
	Size         int64     `xml:"Size"`
	LastModified time.Time `xml:"LastModified"`
}

type gcsListPartsResult struct {
	IsTruncated          bool               `xml:"IsTruncated"`
	NextPartNumberMarker int32              `xml:"NextPartNumberMarker"`
	Parts                []gcsMultipartPart `xml:"Part"`
}

type gcsCompleteMultipartUpload struct {
	XMLName xml.Name           `xml:"CompleteMultipartUpload"`
	Parts   []gcsCompletedPart `xml:"Part"`
}

type gcsCompletedPart struct {
	PartNumber int32  `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

func (p *GCS) xmlRequest(ctx context.Context, method, bucket, key string, query url.Values, headers http.Header, body []byte, out any) error {
	requestURL := *p.endpoint
	requestURL.Path = "/" + bucket
	if key != "" {
		requestURL.Path += "/" + key
	}
	requestURL.RawPath = ""
	requestURL.RawQuery = query.Encode()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL.String(), reader)
	if err != nil {
		return ErrInvalid
	}
	for name, values := range headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	limited := io.LimitReader(resp.Body, gcsMaxControlBody+1)
	data, err := io.ReadAll(limited)
	if err != nil || len(data) > gcsMaxControlBody {
		return ErrUnavailable
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		failure := &gcsHTTPError{status: resp.StatusCode}
		var wire struct {
			Code string `xml:"Code"`
		}
		if xml.Unmarshal(data, &wire) == nil {
			failure.code = wire.Code
		}
		return failure
	}
	if out != nil && len(data) > 0 {
		if err := xml.Unmarshal(data, out); err != nil {
			return ErrUnavailable
		}
	}
	return nil
}

type gcsHTTPError struct {
	status int
	code   string
}

func (*gcsHTTPError) Error() string          { return "GCS request failed" }
func (e *gcsHTTPError) HTTPStatusCode() int  { return e.status }
func (e *gcsHTTPError) ProviderCode() string { return e.code }

func gcsStatusCode(err error) int {
	if err == nil {
		return 0
	}
	var status interface{ HTTPStatusCode() int }
	if errors.As(err, &status) {
		return status.HTTPStatusCode()
	}
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return 0
}

func normalizeGCS(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, storage.ErrBucketNotExist) || errors.Is(err, storage.ErrObjectNotExist) {
		return ErrNotFound
	}
	var coded interface{ ProviderCode() string }
	if errors.As(err, &coded) {
		switch coded.ProviderCode() {
		case "AccessDenied", "AuthenticationRequired", "InvalidSecurity", "SignatureDoesNotMatch":
			return ErrConfiguration
		case "NoSuchBucket", "NoSuchKey", "NoSuchUpload", "NotFound":
			return ErrNotFound
		case "BucketNotEmpty":
			return ErrNotEmpty
		case "InvalidArgument", "InvalidRequest", "InvalidPart", "InvalidPartOrder", "EntityTooSmall", "EntityTooLarge":
			return ErrInvalid
		}
	}
	status := gcsStatusCode(err)
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrConfiguration
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusBadRequest:
		return ErrInvalid
	case http.StatusConflict, http.StatusPreconditionFailed:
		return ErrConflict
	default:
		return ErrUnavailable
	}
}

func normalizeGCSSigning(err error) error {
	status := gcsStatusCode(err)
	if status >= http.StatusBadRequest && status < http.StatusTooManyRequests {
		return ErrConfiguration
	}
	return ErrUnavailable
}

type gcsIAMBlobSigner struct {
	client         *http.Client
	serviceAccount string
	endpoint       string
}

func (s *gcsIAMBlobSigner) Sign(ctx context.Context, payload []byte) ([]byte, error) {
	requestBody, err := json.Marshal(struct {
		Payload string `json:"payload"`
	}{Payload: base64.StdEncoding.EncodeToString(payload)})
	if err != nil {
		return nil, ErrUnavailable
	}
	endpoint := strings.TrimRight(s.endpoint, "/") + "/v1/projects/-/serviceAccounts/" + url.PathEscape(s.serviceAccount) + ":signBlob"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return nil, ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, ErrUnavailable
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, &gcsHTTPError{status: resp.StatusCode}
	}
	var signed struct {
		SignedBlob string `json:"signedBlob"`
	}
	if json.Unmarshal(data, &signed) != nil || signed.SignedBlob == "" {
		return nil, ErrUnavailable
	}
	value, err := base64.StdEncoding.DecodeString(signed.SignedBlob)
	if err != nil || len(value) == 0 {
		return nil, ErrUnavailable
	}
	return value, nil
}
