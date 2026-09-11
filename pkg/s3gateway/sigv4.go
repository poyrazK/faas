package s3gateway

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

const sigV4Algorithm = "AWS4-HMAC-SHA256"

var errSignature = errors.New("s3 gateway: invalid signature")

type sigV4Request struct {
	AccessKeyID  string
	PayloadHash  string
	SignedAt     time.Time
	SignedHeader string
	Signature    string
	ScopeDate    string
}

const maxPresignTTL = 7 * 24 * time.Hour

func parseSigV4(r *http.Request, region string, now time.Time) (sigV4Request, error) {
	authorization := r.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, sigV4Algorithm+" ") || strings.Contains(authorization, "\n") {
		return sigV4Request{}, errSignature
	}
	attributes := map[string]string{}
	for _, part := range strings.Split(strings.TrimPrefix(authorization, sigV4Algorithm+" "), ",") {
		pair := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(pair) != 2 || pair[0] == "" || pair[1] == "" {
			return sigV4Request{}, errSignature
		}
		if _, exists := attributes[pair[0]]; exists {
			return sigV4Request{}, errSignature
		}
		attributes[pair[0]] = pair[1]
	}
	if len(attributes) != 3 {
		return sigV4Request{}, errSignature
	}
	credential := strings.Split(attributes["Credential"], "/")
	if len(credential) != 5 || credential[0] == "" || credential[1] == "" || credential[2] != region || credential[3] != "s3" || credential[4] != "aws4_request" {
		return sigV4Request{}, errSignature
	}
	signedHeaders := strings.Split(attributes["SignedHeaders"], ";")
	if len(signedHeaders) == 0 || !sort.StringsAreSorted(signedHeaders) {
		return sigV4Request{}, errSignature
	}
	seen := map[string]bool{}
	for _, name := range signedHeaders {
		if name == "" || name != strings.ToLower(name) || seen[name] {
			return sigV4Request{}, errSignature
		}
		seen[name] = true
		if name != "host" && name != "content-length" && len(r.Header.Values(name)) == 0 {
			return sigV4Request{}, errSignature
		}
	}
	if !seen["host"] || !seen["x-amz-date"] || !seen["x-amz-content-sha256"] {
		return sigV4Request{}, errSignature
	}
	dateValue := r.Header.Get("X-Amz-Date")
	signedAt, err := time.Parse("20060102T150405Z", dateValue)
	if err != nil || credential[1] != signedAt.UTC().Format("20060102") || signedAt.Before(now.Add(-15*time.Minute)) || signedAt.After(now.Add(15*time.Minute)) {
		return sigV4Request{}, errSignature
	}
	payloadHash := r.Header.Get("X-Amz-Content-Sha256")
	if payloadHash != "UNSIGNED-PAYLOAD" {
		decoded, err := hex.DecodeString(payloadHash)
		if err != nil || len(decoded) != 32 || payloadHash != strings.ToLower(payloadHash) {
			return sigV4Request{}, errSignature
		}
	}
	signature := attributes["Signature"]
	decodedSignature, err := hex.DecodeString(signature)
	if err != nil || len(decodedSignature) != 32 || signature != strings.ToLower(signature) {
		return sigV4Request{}, errSignature
	}
	return sigV4Request{
		AccessKeyID: credential[0], PayloadHash: payloadHash, SignedAt: signedAt,
		SignedHeader: attributes["SignedHeaders"], Signature: signature, ScopeDate: credential[1],
	}, nil
}

// parsePresignedSigV4 validates the non-cryptographic parts of a query
// signature and returns the same request shape used by header signatures.
// Cryptographic verification is deliberately separate because the credential
// secret is resolved by the caller after the access key has been parsed.
func parsePresignedSigV4(r *http.Request, region string, now time.Time) (sigV4Request, error) {
	query := r.URL.Query()
	if query.Get("X-Amz-Algorithm") != sigV4Algorithm || len(query["X-Amz-Algorithm"]) != 1 {
		return sigV4Request{}, errSignature
	}
	credential := strings.Split(query.Get("X-Amz-Credential"), "/")
	if len(credential) != 5 || credential[0] == "" || credential[1] == "" || credential[2] != region || credential[3] != "s3" || credential[4] != "aws4_request" {
		return sigV4Request{}, errSignature
	}
	if len(query["X-Amz-Credential"]) != 1 || len(query["X-Amz-Date"]) != 1 || len(query["X-Amz-Expires"]) != 1 || len(query["X-Amz-SignedHeaders"]) != 1 || len(query["X-Amz-Signature"]) != 1 {
		return sigV4Request{}, errSignature
	}
	signedAt, err := time.Parse("20060102T150405Z", query.Get("X-Amz-Date"))
	if err != nil || credential[1] != signedAt.UTC().Format("20060102") {
		return sigV4Request{}, errSignature
	}
	expires, err := strconv.ParseInt(query.Get("X-Amz-Expires"), 10, 64)
	if err != nil || expires < 1 || expires > int64(maxPresignTTL/time.Second) {
		return sigV4Request{}, errSignature
	}
	if signedAt.After(now.Add(15*time.Minute)) || now.After(signedAt.Add(time.Duration(expires)*time.Second)) {
		return sigV4Request{}, errSignature
	}
	signedHeaders := strings.Split(query.Get("X-Amz-SignedHeaders"), ";")
	if len(signedHeaders) == 0 || !sort.StringsAreSorted(signedHeaders) {
		return sigV4Request{}, errSignature
	}
	seen := map[string]bool{}
	for _, name := range signedHeaders {
		if name == "" || name != strings.ToLower(name) || seen[name] {
			return sigV4Request{}, errSignature
		}
		seen[name] = true
		if name != "host" && name != "content-length" && headerOrQueryValue(r, name) == "" {
			return sigV4Request{}, errSignature
		}
	}
	if !seen["host"] {
		return sigV4Request{}, errSignature
	}
	payloadHash := r.Header.Get("X-Amz-Content-Sha256")
	if payloadHash == "" {
		payloadHash = queryValue(query, "x-amz-content-sha256")
	}
	if payloadHash == "" {
		payloadHash = "UNSIGNED-PAYLOAD"
	}
	if strings.HasPrefix(payloadHash, "STREAMING-") {
		return sigV4Request{}, errSignature
	}
	if payloadHash != "UNSIGNED-PAYLOAD" {
		decoded, err := hex.DecodeString(payloadHash)
		if err != nil || len(decoded) != 32 || payloadHash != strings.ToLower(payloadHash) {
			return sigV4Request{}, errSignature
		}
	}
	signature := query.Get("X-Amz-Signature")
	decodedSignature, err := hex.DecodeString(signature)
	if err != nil || len(decodedSignature) != 32 || signature != strings.ToLower(signature) {
		return sigV4Request{}, errSignature
	}
	return sigV4Request{
		AccessKeyID: credential[0], PayloadHash: payloadHash, SignedAt: signedAt,
		SignedHeader: query.Get("X-Amz-SignedHeaders"), Signature: signature, ScopeDate: credential[1],
	}, nil
}

// verifyPresignedSigV4 rebuilds the canonical query signature using the AWS
// signer. The incoming signature parameter is removed before rebuilding; all
// other query parameters remain part of the canonical request.
func verifyPresignedSigV4(ctx context.Context, r *http.Request, parsed sigV4Request, secret, region string) error {
	if secret == "" {
		return errSignature
	}
	clone := r.Clone(ctx)
	clone.Body, clone.GetBody = nil, nil
	originalHeaders := r.Header
	clone.Header = make(http.Header)
	for _, name := range strings.Split(parsed.SignedHeader, ";") {
		if name == "host" || name == "content-length" {
			continue
		}
		for _, value := range originalHeaders.Values(name) {
			clone.Header.Add(name, value)
		}
		if clone.Header.Get(name) == "" {
			if value := queryValue(clone.URL.Query(), name); value != "" {
				clone.Header.Set(name, value)
			}
		}
	}
	if slices.Contains(strings.Split(parsed.SignedHeader, ";"), "content-length") {
		clone.ContentLength = r.ContentLength
	}
	query := clone.URL.Query()
	query.Del("X-Amz-Signature")
	clone.URL.RawQuery = query.Encode()
	signed, _, err := awsv4.NewSigner().PresignHTTP(ctx, aws.Credentials{AccessKeyID: parsed.AccessKeyID, SecretAccessKey: secret}, clone, parsed.PayloadHash, "s3", region, parsed.SignedAt)
	if err != nil {
		return errSignature
	}
	signedURL, err := url.Parse(signed)
	if err != nil {
		return errSignature
	}
	calculated := signedURL.Query().Get("X-Amz-Signature")
	if subtle.ConstantTimeCompare([]byte(calculated), []byte(parsed.Signature)) != 1 {
		return errSignature
	}
	return nil
}

func queryValue(query url.Values, name string) string {
	for key, values := range query {
		if strings.EqualFold(key, name) && len(values) == 1 {
			return values[0]
		}
	}
	return ""
}

func headerOrQueryValue(r *http.Request, name string) string {
	if value := r.Header.Get(name); value != "" {
		return value
	}
	return queryValue(r.URL.Query(), name)
}

func verifySigV4(ctx context.Context, r *http.Request, parsed sigV4Request, secret, region string) error {
	if secret == "" {
		return errSignature
	}
	clone := r.Clone(ctx)
	clone.Body, clone.GetBody = nil, nil
	clone.Header = make(http.Header)
	clone.Host = r.Host
	for _, name := range strings.Split(parsed.SignedHeader, ";") {
		switch name {
		case "host":
			continue
		case "content-length":
			clone.ContentLength = r.ContentLength
		default:
			for _, value := range r.Header.Values(name) {
				clone.Header.Add(name, value)
			}
		}
	}
	clone.Header.Del("Authorization")
	signer := awsv4.NewSigner()
	if err := signer.SignHTTP(ctx, aws.Credentials{AccessKeyID: parsed.AccessKeyID, SecretAccessKey: secret}, clone, parsed.PayloadHash, "s3", region, parsed.SignedAt); err != nil {
		return errSignature
	}
	calculated, err := parseSignatureOnly(clone.Header.Get("Authorization"))
	if err != nil || subtle.ConstantTimeCompare([]byte(calculated), []byte(parsed.Signature)) != 1 {
		return errSignature
	}
	return nil
}

func parseSignatureOnly(authorization string) (string, error) {
	for _, part := range strings.Split(authorization, ",") {
		pair := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(pair) == 2 && pair[0] == "Signature" {
			return pair[1], nil
		}
	}
	return "", errSignature
}
