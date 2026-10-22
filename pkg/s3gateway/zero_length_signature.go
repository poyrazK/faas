package s3gateway

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// The Go SDK signer always drops Content-Length: 0, including explicit headers.
// This narrowly scoped verifier retains it according to the SigV4 contract:
// https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv-create-signed-request.html
// All date/scope/header/payload validation still precedes this function.
func verifyZeroLengthSigV4(r *http.Request, parsed sigV4Request, secret, region string, presigned bool) error {
	var headers strings.Builder
	for _, name := range strings.Split(parsed.SignedHeader, ";") {
		values := r.Header.Values(name)
		switch name {
		case "host":
			values = []string{r.Host}
		case "content-length":
			values = []string{"0"}
		default:
			if len(values) == 0 && presigned {
				values = []string{queryValue(r.URL.Query(), name)}
			}
		}
		cleaned := make([]string, len(values))
		for i, value := range values {
			cleaned[i] = strings.Join(strings.Fields(value), " ")
		}
		headers.WriteString(name + ":" + strings.Join(cleaned, ",") + "\n")
	}
	query := r.URL.Query()
	if presigned {
		query.Del("X-Amz-Signature")
	}
	var parameters []string
	encode := func(value string) string { return strings.ReplaceAll(url.QueryEscape(value), "+", "%20") }
	for name, values := range query {
		for _, value := range values {
			parameters = append(parameters, encode(name)+"="+encode(value))
		}
	}
	sort.Strings(parameters)
	path := r.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	canonical := strings.Join([]string{r.Method, path, strings.Join(parameters, "&"), headers.String(), parsed.SignedHeader, parsed.PayloadHash}, "\n")
	hash := sha256.Sum256([]byte(canonical))
	scope := parsed.ScopeDate + "/" + region + "/s3/aws4_request"
	toSign := strings.Join([]string{sigV4Algorithm, parsed.SignedAt.UTC().Format("20060102T150405Z"), scope, hex.EncodeToString(hash[:])}, "\n")
	calculated := hex.EncodeToString(hmacSHA256(deriveSigV4Key(secret, parsed.ScopeDate, region, "s3"), toSign))
	if subtle.ConstantTimeCompare([]byte(calculated), []byte(parsed.Signature)) != 1 {
		return errSignature
	}
	return nil
}
