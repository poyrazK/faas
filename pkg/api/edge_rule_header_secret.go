package api

import (
	"net/textproto"
	"strings"
)

// EdgeRuleHeaderSecretSealNamespace is the secretbox namespace of a sealed
// edge-rule request header value (ADR-20261108110000); apid seals under it
// and gatewayd-internal refuses a value sealed under any other.
const EdgeRuleHeaderSecretSealNamespace = "EDGE_RULE_HEADER_VALUE"

// EdgeRuleHeaderSecretMaxBytes bounds a sealed header value. Header values
// past a few KiB are refused by most origins anyway.
const EdgeRuleHeaderSecretMaxBytes = 4096

// edgeRuleCredentialHeaders are request headers whose value is a credential
// by definition.
var edgeRuleCredentialHeaders = map[string]struct{}{
	"Authorization":       {},
	"Proxy-Authorization": {},
	"Cookie":              {},
}

// edgeRuleCredentialHeaderWords mark a header name as carrying a credential.
var edgeRuleCredentialHeaderWords = []string{"token", "secret", "password", "api-key", "apikey", "signature", "credential"}

// EdgeRuleHeaderNameIsCredential reports whether a request header op on name
// is sealed without being marked secret (ADR-20261108110000): Authorization,
// Proxy-Authorization, Cookie, or a name containing token, secret, password,
// api-key, apikey, signature or credential.
func EdgeRuleHeaderNameIsCredential(name string) bool {
	if _, ok := edgeRuleCredentialHeaders[textproto.CanonicalMIMEHeaderKey(name)]; ok {
		return true
	}
	lower := strings.ToLower(name)
	for _, word := range edgeRuleCredentialHeaderWords {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

// EdgeRuleHeaderOpSealed reports whether a request header op's value is kept
// sealed: marked secret, already sealed, or a literal on a credential-named
// header. Templates and remove ops carry no value to seal.
func EdgeRuleHeaderOpSealed(op EdgeRuleHeaderOp) bool {
	if op.Template || op.Action == "remove" {
		return false
	}
	return op.Secret || op.SecretID != "" || EdgeRuleHeaderNameIsCredential(op.Name)
}

// validateSecretFields checks the ADR-20261108110000 fields of an op; request
// is false for a response header op.
func (op *EdgeRuleHeaderOp) validateSecretFields(request bool) *Problem {
	if !op.Secret && op.SecretID == "" && !op.Redacted {
		return nil
	}
	switch {
	case !request:
		return ErrValidation("header op secret applies only to request_headers: response headers are sent to the client")
	case op.Action == "remove":
		return ErrValidation("header op secret applies only to add and set")
	case op.Template:
		return ErrValidation("header op secret and template cannot be combined: a template reads request values")
	case len(op.Value) > EdgeRuleHeaderSecretMaxBytes:
		return ErrValidation("header op secret value must be at most 4096 bytes")
	}
	return nil
}
