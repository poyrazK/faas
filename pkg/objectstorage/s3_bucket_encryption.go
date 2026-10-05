package objectstorage

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ BucketEncryptionProvider = (*S3)(nil)

func (p *S3) GetBucketEncryption(ctx context.Context, bucket string) (NativeBucketEncryption, error) {
	out, err := p.client.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{Bucket: aws.String(bucket)}, func(o *s3.Options) {
		o.RetryMaxAttempts = 1
		o.HTTPClient = bucketEncryptionReadClient{base: o.HTTPClient}
	})
	var service smithy.APIError
	if errors.As(err, &service) && service.ErrorCode() == "ServerSideEncryptionConfigurationNotFoundError" {
		return NativeBucketEncryption{}, nil
	}
	if err != nil {
		return NativeBucketEncryption{}, normalizeVersionHistoryError(err)
	}
	if out == nil || out.ServerSideEncryptionConfiguration == nil || len(out.ServerSideEncryptionConfiguration.Rules) != 1 {
		return NativeBucketEncryption{}, ErrUnavailable
	}
	rule := out.ServerSideEncryptionConfiguration.Rules[0]
	if rule.ApplyServerSideEncryptionByDefault == nil {
		return NativeBucketEncryption{}, ErrUnavailable
	}
	n := NativeBucketEncryption{Algorithm: string(rule.ApplyServerSideEncryptionByDefault.SSEAlgorithm), KeyID: aws.ToString(rule.ApplyServerSideEncryptionByDefault.KMSMasterKeyID), BucketKeyEnabled: rule.BucketKeyEnabled}
	if rule.BlockedEncryptionTypes != nil {
		for _, kind := range rule.BlockedEncryptionTypes.EncryptionType {
			n.BlockedEncryptionTypes = append(n.BlockedEncryptionTypes, string(kind))
		}
	}
	if !validNativeBucketEncryption(n) {
		return NativeBucketEncryption{}, ErrUnavailable
	}
	if n.Algorithm != "aws:kms" {
		n.BucketKeyEnabled = nil
	}
	slices.Sort(n.BlockedEncryptionTypes)
	return n.Clone(), nil
}

func validNativeBucketEncryption(n NativeBucketEncryption) bool {
	if n.Algorithm != "AES256" && n.Algorithm != "aws:kms" && n.Algorithm != "aws:kms:dsse" || len(n.KeyID) > api.MaxObjectEncryptionKeyRefBytes || strings.ContainsAny(n.KeyID, "\x00\r\n") {
		return false
	}
	if n.Algorithm == "AES256" && n.KeyID != "" || n.Algorithm != "aws:kms" && n.BucketKeyEnabled != nil && *n.BucketKeyEnabled {
		return false
	}
	if len(n.BlockedEncryptionTypes) > 2 {
		return false
	}
	seen := map[string]bool{}
	for _, kind := range n.BlockedEncryptionTypes {
		if kind != "SSE-C" && kind != "NONE" || seen[kind] {
			return false
		}
		seen[kind] = true
	}
	return true
}

func (p *S3) PutBucketEncryption(ctx context.Context, bucket string, e ResolvedObjectEncryption, current NativeBucketEncryption) error {
	if e.Selection.Context != "" || !validNativeBucketSettings(current) {
		return ErrInvalid
	}
	if err := p.CheckEncryptionKey(ctx, e); err != nil {
		return err
	}
	if err := beforeEncryptionWrite(ctx); err != nil {
		return err
	}
	return p.putBucketEncryption(ctx, bucket, NativeBucketEncryption{Algorithm: e.Selection.Algorithm, KeyID: e.ProviderKeyID, BucketKeyEnabled: e.Selection.BucketKeyEnabled, BlockedEncryptionTypes: slices.Clone(current.BlockedEncryptionTypes)})
}

func validNativeBucketSettings(n NativeBucketEncryption) bool {
	if n.Algorithm == "" {
		return n.KeyID == "" && n.BucketKeyEnabled == nil && len(n.BlockedEncryptionTypes) == 0
	}
	return validNativeBucketEncryption(n)
}

func (p *S3) putBucketEncryption(ctx context.Context, bucket string, n NativeBucketEncryption) error {
	if !validNativeBucketEncryption(n) {
		return ErrInvalid
	}
	apply := &types.ServerSideEncryptionByDefault{SSEAlgorithm: types.ServerSideEncryption(n.Algorithm)}
	if n.KeyID != "" {
		apply.KMSMasterKeyID = aws.String(n.KeyID)
	}
	rule := types.ServerSideEncryptionRule{ApplyServerSideEncryptionByDefault: apply, BucketKeyEnabled: n.BucketKeyEnabled}
	if len(n.BlockedEncryptionTypes) > 0 {
		rule.BlockedEncryptionTypes = &types.BlockedEncryptionTypes{}
		for _, kind := range n.BlockedEncryptionTypes {
			rule.BlockedEncryptionTypes.EncryptionType = append(rule.BlockedEncryptionTypes.EncryptionType, types.EncryptionType(kind))
		}
	}
	_, err := p.client.PutBucketEncryption(ctx, &s3.PutBucketEncryptionInput{Bucket: aws.String(bucket), ServerSideEncryptionConfiguration: &types.ServerSideEncryptionConfiguration{Rules: []types.ServerSideEncryptionRule{rule}}}, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	return normalizeVersionHistoryError(err)
}

func (p *S3) ClearBucketEncryption(ctx context.Context, bucket string, current NativeBucketEncryption) error {
	if !validNativeBucketSettings(current) {
		return ErrInvalid
	}
	if err := beforeEncryptionWrite(ctx); err != nil {
		return err
	}
	// Native encryption blocking shares the configuration document. Clearing
	// the owned cipher keeps those protections and the provider's AES baseline.
	if len(current.BlockedEncryptionTypes) > 0 {
		return p.putBucketEncryption(ctx, bucket, NativeBucketEncryption{Algorithm: "AES256", BlockedEncryptionTypes: slices.Clone(current.BlockedEncryptionTypes)})
	}
	_, err := p.client.DeleteBucketEncryption(ctx, &s3.DeleteBucketEncryptionInput{Bucket: aws.String(bucket)}, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	return normalizeVersionHistoryError(err)
}

type bucketEncryptionReadClient struct{ base aws.HTTPClient }

func (c bucketEncryptionReadClient) Do(r *http.Request) (*http.Response, error) {
	response, err := c.base.Do(r)
	if err != nil {
		return response, err
	}
	if response == nil || response.Body == nil {
		return nil, ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, api.MaxObjectBucketEncryptionBodyBytes+1))
	_ = response.Body.Close()
	if err != nil || int64(len(body)) > api.MaxObjectBucketEncryptionBodyBytes {
		return nil, ErrUnavailable
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 && !validBucketEncryptionResponse(body) {
		return nil, ErrUnavailable
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
}

type bucketEncryptionXMLDefault struct {
	Algorithm []string                     `xml:"SSEAlgorithm"`
	Key       []string                     `xml:"KMSMasterKeyID"`
	Extra     []struct{ XMLName xml.Name } `xml:",any"`
}
type bucketEncryptionXMLBlocked struct {
	Types []string                     `xml:"EncryptionType"`
	Extra []struct{ XMLName xml.Name } `xml:",any"`
}
type bucketEncryptionXMLRule struct {
	Apply     []bucketEncryptionXMLDefault `xml:"ApplyServerSideEncryptionByDefault"`
	BucketKey []string                     `xml:"BucketKeyEnabled"`
	Blocked   []bucketEncryptionXMLBlocked `xml:"BlockedEncryptionTypes"`
	Extra     []struct{ XMLName xml.Name } `xml:",any"`
}

func validBucketEncryptionResponse(body []byte) bool {
	if !validBucketEncryptionNamespaces(body) {
		return false
	}
	var in struct {
		XMLName xml.Name
		Rules   []bucketEncryptionXMLRule    `xml:"Rule"`
		Extra   []struct{ XMLName xml.Name } `xml:",any"`
	}
	d := xml.NewDecoder(bytes.NewReader(body))
	if d.Decode(&in) != nil || in.XMLName.Local != "ServerSideEncryptionConfiguration" || in.XMLName.Space != "" && in.XMLName.Space != "http://s3.amazonaws.com/doc/2006-03-01/" || len(in.Rules) != 1 || len(in.Extra) != 0 {
		return false
	}
	rule := in.Rules[0]
	if len(rule.Apply) != 1 || len(rule.BucketKey) > 1 || len(rule.Blocked) > 1 || len(rule.Extra) > 0 {
		return false
	}
	apply := rule.Apply[0]
	if len(apply.Algorithm) != 1 || len(apply.Key) > 1 || len(apply.Extra) > 0 {
		return false
	}
	n := NativeBucketEncryption{Algorithm: apply.Algorithm[0]}
	if len(apply.Key) > 0 {
		n.KeyID = apply.Key[0]
	}
	if len(rule.BucketKey) > 0 {
		value := rule.BucketKey[0] == "true"
		if rule.BucketKey[0] != "true" && rule.BucketKey[0] != "false" {
			return false
		}
		n.BucketKeyEnabled = &value
	}
	if len(rule.Blocked) > 0 {
		if len(rule.Blocked[0].Types) == 0 || len(rule.Blocked[0].Extra) > 0 {
			return false
		}
		n.BlockedEncryptionTypes = rule.Blocked[0].Types
	}
	if !validNativeBucketEncryption(n) {
		return false
	}
	return errors.Is(d.Decode(new(any)), io.EOF)
}

func validBucketEncryptionNamespaces(body []byte) bool {
	d := xml.NewDecoder(bytes.NewReader(body))
	for {
		token, err := d.Token()
		if errors.Is(err, io.EOF) {
			return true
		}
		if err != nil {
			return false
		}
		if start, ok := token.(xml.StartElement); ok {
			if start.Name.Space != "" && start.Name.Space != "http://s3.amazonaws.com/doc/2006-03-01/" {
				return false
			}
			for _, attr := range start.Attr {
				if attr.Name.Local != "xmlns" && attr.Name.Space != "xmlns" {
					return false
				}
			}
		}
	}
}

// DecodeBucketEncryptionConfiguration accepts an owned public default, without
// per-object contexts or changes to unrelated native encryption protections.
func DecodeBucketEncryptionConfiguration(body []byte) (api.ObjectEncryption, error) {
	if int64(len(body)) > api.MaxObjectBucketEncryptionBodyBytes || !validBucketEncryptionResponse(body) {
		return api.ObjectEncryption{}, ErrInvalid
	}
	var in struct {
		Rules []bucketEncryptionXMLRule `xml:"Rule"`
	}
	if xml.Unmarshal(body, &in) != nil {
		return api.ObjectEncryption{}, ErrInvalid
	}
	rule := in.Rules[0]
	if len(rule.Blocked) > 0 {
		return api.ObjectEncryption{}, ErrUnsupported
	}
	apply := rule.Apply[0]
	e := api.ObjectEncryption{Algorithm: apply.Algorithm[0]}
	if len(apply.Key) > 0 {
		e.KeyID = apply.Key[0]
	}
	if e.Algorithm == "aws:kms" && len(rule.BucketKey) > 0 {
		value := rule.BucketKey[0] == "true"
		e.BucketKeyEnabled = &value
	}
	if e.Empty() || !e.Valid() {
		return api.ObjectEncryption{}, ErrInvalid
	}
	return e, nil
}
