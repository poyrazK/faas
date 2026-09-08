package s3gateway

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"sort"
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
