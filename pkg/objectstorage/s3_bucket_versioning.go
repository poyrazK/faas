package objectstorage

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"io"
	"net/http"
)

var _ BucketVersioningProvider = (*S3)(nil)

func (p *S3) GetBucketVersioning(ctx context.Context, bucket string) (BucketVersioning, error) {
	out, err := p.client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: aws.String(bucket)}, func(o *s3.Options) { o.RetryMaxAttempts = 1; o.HTTPClient = versioningReadClient{base: o.HTTPClient} })
	if err != nil {
		return BucketVersioning{}, normalizeVersionHistoryError(err)
	}
	if out == nil {
		return BucketVersioning{}, ErrUnavailable
	}
	v := BucketVersioning{Status: string(out.Status), MFADelete: string(out.MFADelete)}
	if v.Status != "" && !state.ValidObjectBucketVersioningStatus(v.Status) || v.MFADelete != "" && v.MFADelete != "Enabled" && v.MFADelete != "Disabled" {
		return BucketVersioning{}, ErrUnavailable
	}
	return v, nil
}
func (p *S3) PutBucketVersioning(ctx context.Context, bucket, status string) error {
	if !state.ValidObjectBucketVersioningStatus(status) {
		return ErrInvalid
	}
	_, err := p.client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{Bucket: aws.String(bucket), VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatus(status)}}, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	return normalizeVersionHistoryError(err)
}

// Validate the bounded XML before the SDK interprets an empty configuration.
// Unknown/duplicate fields and an Error document must never become unversioned.
type versioningReadClient struct{ base aws.HTTPClient }

func (c versioningReadClient) Do(r *http.Request) (*http.Response, error) {
	response, err := c.base.Do(r)
	if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return response, err
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, api.MaxObjectBucketVersioningBodyBytes+1))
	_ = response.Body.Close()
	if err != nil || int64(len(body)) > api.MaxObjectBucketVersioningBodyBytes || !validVersioningResponse(body) {
		return nil, ErrUnavailable
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
}
func validVersioningResponse(body []byte) bool {
	var in struct {
		XMLName xml.Name
		Status  []string                     `xml:"Status"`
		MFA     []string                     `xml:"MfaDelete"`
		Extra   []struct{ XMLName xml.Name } `xml:",any"`
	}
	d := xml.NewDecoder(bytes.NewReader(body))
	if d.Decode(&in) != nil || in.XMLName.Local != "VersioningConfiguration" || in.XMLName.Space != "" && in.XMLName.Space != "http://s3.amazonaws.com/doc/2006-03-01/" || len(in.Status) > 1 || len(in.MFA) > 1 || len(in.Extra) > 0 {
		return false
	}
	if len(in.Status) == 1 && !state.ValidObjectBucketVersioningStatus(in.Status[0]) || len(in.MFA) == 1 && in.MFA[0] != "Enabled" && in.MFA[0] != "Disabled" {
		return false
	}
	var extra any
	return errors.Is(d.Decode(&extra), io.EOF)
}
