package objectstorage

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ VersionReadPresigner = (*GCS)(nil)
var _ ObjectVersionLister = (*GCS)(nil)

func (p *GCS) PresignVersionRead(ctx context.Context, bucket, method, key, generation string, checksum bool, expires int64) (SignedRequest, error) {
	if _, err := gcsGeneration(generation); err != nil || bucket == "" || (SignRequest{Method: method, Key: key, ExpiresIn: expires}).Validate(api.MaxObjectSinglePutBytes) != nil || method != http.MethodGet && method != http.MethodHead {
		return SignedRequest{}, ErrInvalid
	}
	if checksum {
		return SignedRequest{}, ErrUnsupported
	}
	ttl := time.Duration(expires) * time.Second
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	at := p.now().Add(ttl)
	opts := storage.SignedURLOptions{GoogleAccessID: p.serviceAccount, Method: method, Expires: at, Scheme: storage.SigningSchemeV4, Style: storage.PathStyle(), QueryParameters: url.Values{"generation": {generation}}}
	signed, err := p.signedURL(ctx, bucket, key, opts)
	if err != nil {
		return SignedRequest{}, err
	}
	return SignedRequest{URL: signed, Method: method, Headers: map[string]string{"Accept-Encoding": "gzip"}, ExpiresAt: at}, nil
}

type gcsVersionListXML struct {
	XMLName     xml.Name              `xml:"ListVersionsResult"`
	Encoding    string                `xml:"EncodingType"`
	Truncated   *bool                 `xml:"IsTruncated"`
	NextKey     string                `xml:"NextKeyMarker"`
	NextVersion string                `xml:"NextVersionIdMarker"`
	Versions    []gcsListedVersionXML `xml:"Version"`
	Markers     []struct{}            `xml:"DeleteMarker"`
	Prefixes    []struct {
		Prefix string `xml:"Prefix"`
	} `xml:"CommonPrefixes"`
}
type gcsListedVersionXML struct {
	Key      string    `xml:"Key"`
	ID       string    `xml:"VersionId"`
	ETag     string    `xml:"ETag"`
	Size     *int64    `xml:"Size"`
	Latest   *bool     `xml:"IsLatest"`
	Modified time.Time `xml:"LastModified"`
}

func (p *GCS) ListObjectVersionPage(ctx context.Context, bucket string, r ObjectVersionListRequest) (ObjectVersionListPage, error) {
	if !validGCSVersionListRequest(bucket, r) {
		return ObjectVersionListPage{}, ErrInvalid
	}
	q := url.Values{"versions": {""}, "encoding-type": {"url"}, "max-keys": {strconv.Itoa(int(r.Limit))}}
	for name, value := range map[string]string{"prefix": r.Prefix, "delimiter": r.Delimiter, "key-marker": r.KeyMarker, "version-id-marker": r.ProviderVersionMarker} {
		if value != "" {
			q.Set(name, value)
		}
	}
	var out gcsVersionListXML
	h := http.Header{"x-goog-interop-list-objects-format": {"enabled"}}
	if err := p.xmlRequest(ctx, http.MethodGet, bucket, "", q, h, nil, &out); err != nil {
		return ObjectVersionListPage{}, normalizeGCS(err)
	}
	return parseGCSVersionList(out, r)
}

func validGCSVersionListRequest(bucket string, r ObjectVersionListRequest) bool {
	if bucket == "" || r.Limit < 1 || r.Limit > api.MaxObjectS3ListItems || !validVersionListText(r.Prefix) || !validVersionListText(r.KeyMarker) || !validVersionDelimiter(r.Delimiter) {
		return false
	}
	if r.ProviderVersionMarker != "" {
		if _, err := gcsGeneration(r.ProviderVersionMarker); err != nil || r.KeyMarker == "" {
			return false
		}
	}
	return true
}

func gcsVersionListKey(key, encoding string) (string, error) {
	if encoding == "url" {
		var err error
		key, err = url.PathUnescape(key)
		if err != nil {
			return "", ErrUnavailable
		}
	} else if encoding != "" {
		return "", ErrUnavailable
	}
	if key == "" || !validVersionListText(key) {
		return "", ErrUnavailable
	}
	return key, nil
}

func parseGCSVersionList(out gcsVersionListXML, r ObjectVersionListRequest) (ObjectVersionListPage, error) {
	page := ObjectVersionListPage{Items: []ListedObjectVersion{}, CommonPrefixes: []string{}}
	if out.Truncated == nil || len(out.Markers) != 0 || len(out.Versions)+len(out.Prefixes) > int(r.Limit) {
		return page, ErrUnavailable
	}
	seen := map[string]bool{}
	for _, v := range out.Versions {
		key, err := gcsVersionListKey(v.Key, out.Encoding)
		_, genErr := gcsGeneration(v.ID)
		if err != nil || genErr != nil || !strings.HasPrefix(key, r.Prefix) || v.Size == nil || *v.Size < 0 || *v.Size > api.MaxObjectUploadBytes || v.Latest == nil || v.Modified.IsZero() || !validUploadETag(v.ETag) || seen[key+"\x00"+v.ID] {
			return ObjectVersionListPage{}, ErrUnavailable
		}
		seen[key+"\x00"+v.ID] = true
		page.Items = append(page.Items, ListedObjectVersion{Object: Object{Key: key, ETag: v.ETag, Size: *v.Size, LastModified: v.Modified}, ProviderVersionID: v.ID, IsLatest: *v.Latest, StorageClass: "STANDARD"})
	}
	for _, p := range out.Prefixes {
		key, err := gcsVersionListKey(p.Prefix, out.Encoding)
		if err != nil || r.Delimiter == "" || !strings.HasPrefix(key, r.Prefix) || !strings.HasSuffix(key, r.Delimiter) || seen["prefix\x00"+key] {
			return ObjectVersionListPage{}, ErrUnavailable
		}
		seen["prefix\x00"+key] = true
		page.CommonPrefixes = append(page.CommonPrefixes, key)
	}
	if err := finishGCSVersionList(&page, out, r); err != nil {
		return ObjectVersionListPage{}, err
	}
	return page, nil
}

func finishGCSVersionList(page *ObjectVersionListPage, out gcsVersionListXML, r ObjectVersionListRequest) error {
	if !*out.Truncated {
		if out.NextKey != "" || out.NextVersion != "" {
			return ErrUnavailable
		}
		return nil
	}
	key, err := gcsVersionListKey(out.NextKey, out.Encoding)
	if err != nil || !strings.HasPrefix(key, r.Prefix) || key == r.KeyMarker && out.NextVersion == r.ProviderVersionMarker || len(page.Items)+len(page.CommonPrefixes) == 0 {
		return ErrUnavailable
	}
	if out.NextVersion != "" {
		if _, err = gcsGeneration(out.NextVersion); err != nil {
			return ErrUnavailable
		}
	}
	page.NextKeyMarker, page.NextProviderVersionMarker = key, out.NextVersion
	return nil
}

// Native generations are translated only after proving the response identity.
func ProviderReadVersionHeader(p Provider, response *http.Response) (string, error) {
	if _, ok := p.(*GCS); !ok {
		return response.Header.Get("X-Amz-Version-Id"), nil
	}
	values := encryptionHeaderValues(response.Header, "X-Goog-Generation")
	if response.Uncompressed {
		return "", ErrUnavailable
	}
	if len(encryptionHeaderValues(response.Header, "X-Amz-Version-Id")) != 0 || len(encryptionHeaderValues(response.Header, "X-Amz-Delete-Marker")) != 0 || len(values) > 1 {
		return "", ErrUnavailable
	}
	if len(values) == 0 {
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return "", ErrUnavailable
		}
		return "", nil
	}
	if _, err := gcsGeneration(values[0]); err != nil {
		return "", ErrUnavailable
	}
	return values[0], nil
}
