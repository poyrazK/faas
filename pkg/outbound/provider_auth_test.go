package outbound

// adr: 984 — provider credential schemes: header styles, SigV4, Vertex.

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// The "get-vanilla" case of the AWS Signature Version 4 test suite.
func TestSignAWSRequestMatchesTheSigV4TestSuite(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://example.amazonaws.com/", nil)
	req.Header = http.Header{}
	key := awsCredential{accessKeyID: "AKIDEXAMPLE", secretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"}
	now := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)
	req.Host = "example.amazonaws.com"
	if err := signAWSRequestRegion(req, nil, key, "service", "us-east-1", now); err != nil {
		t.Fatal(err)
	}
	want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, " +
		"Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("Authorization = %s", got)
	}
}

func TestSignAWSRequestDropsCallerAmzHeadersAndAddsSessionToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://bedrock-runtime.eu-west-1.amazonaws.com/model/m:0/converse", strings.NewReader("{}"))
	req.Header.Set("X-Amz-Security-Token", "caller")
	req.Header.Set("X-Amz-Target", "caller")
	req.Header.Set("Content-Type", "application/json")
	key, err := parseAWSCredential("aws:AKID:secret:session")
	if err != nil {
		t.Fatal(err)
	}
	if err := signAWSRequest(req, []byte("{}"), key, "bedrock", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	auth := req.Header.Get("Authorization")
	if req.Header.Get("X-Amz-Target") != "" || req.Header.Get("X-Amz-Security-Token") != "session" ||
		!strings.Contains(auth, "/eu-west-1/bedrock/aws4_request") || !strings.Contains(auth, "SignedHeaders=content-type;host;x-amz-date;x-amz-security-token") {
		t.Fatalf("headers = %v", req.Header)
	}
	if got := awsCanonicalURI("/model/m:0/converse"); got != "/model/m%3A0/converse" {
		t.Fatalf("canonical URI = %s", got)
	}
	if got := awsCanonicalURI("/model/arn%3Ax%2Fy/invoke"); got != "/model/arn%253Ax%252Fy/invoke" {
		t.Fatalf("canonical URI of an escaped path = %s", got)
	}
}

func TestAWSCredentialAndRegionParsing(t *testing.T) {
	for _, bad := range []string{"", "Bearer x", "aws:", "aws:AKID", "aws::secret"} {
		if _, err := parseAWSCredential(bad); err == nil {
			t.Errorf("parseAWSCredential(%q) accepted", bad)
		}
	}
	for host, want := range map[string]string{
		"bedrock-runtime.us-east-1.amazonaws.com":      "us-east-1",
		"bedrock-runtime-fips.us-west-2.amazonaws.com": "us-west-2",
		"bedrock-runtime.cn-north-1.amazonaws.com.cn":  "cn-north-1",
		"example.com":                     "",
		"bedrock.us-east-1.amazonaws.com": "",
	} {
		if got, _ := awsRegionFromHost(host); got != want {
			t.Errorf("region(%s) = %q, want %q", host, got, want)
		}
	}
}

func TestApplyProviderCredentialHeaderStyles(t *testing.T) {
	h := &Handler{}
	for _, tc := range []struct {
		provider, credential, header, want string
	}{
		{api.OutboundAIProviderOpenAI, "Bearer sk-1", "Authorization", "Bearer sk-1"},
		{api.OutboundAIProviderAnthropic, "Bearer sk-ant", "X-Api-Key", "sk-ant"},
		{api.OutboundAIProviderGemini, "AIza-key", "X-Goog-Api-Key", "AIza-key"},
		{api.OutboundAIProviderAzureOpenAI, "azure-key", "Api-Key", "azure-key"},
		{api.OutboundAIProviderAzureOpenAI, "Bearer entra-token", "Authorization", "Bearer entra-token"},
		{api.OutboundAIProviderVertex, "Bearer ya29.token", "Authorization", "Bearer ya29.token"},
	} {
		req := httptest.NewRequest(http.MethodPost, "https://provider.example/v1", nil)
		req.Header.Set("Authorization", "Bearer caller")
		if err := h.applyProviderCredential(context.Background(), req, Integration{AIProvider: tc.provider}, tc.credential, nil); err != nil {
			t.Fatalf("%s: %v", tc.provider, err)
		}
		if got := req.Header.Get(tc.header); got != tc.want {
			t.Errorf("%s %s = %q", tc.provider, tc.header, got)
		}
		if tc.header != "Authorization" && req.Header.Get("Authorization") != "" {
			t.Errorf("%s kept the caller's Authorization", tc.provider)
		}
	}
}

func TestVertexServiceAccountAssertion(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	account := googleServiceAccount{
		Type: "service_account", ClientEmail: "gw@p.iam.gserviceaccount.com", PrivateKeyID: "kid-1",
		PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
	}
	now := time.Unix(1_800_000_000, 0)
	assertion, err := googleServiceAccountAssertion(account, now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(assertion, ".")
	if len(parts) != 3 {
		t.Fatalf("assertion = %s", assertion)
	}
	signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
		t.Fatalf("signature: %v", err)
	}
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil || claims["aud"] != googleTokenURI || claims["iss"] != account.ClientEmail ||
		claims["scope"] != vertexTokenScope || claims["exp"].(float64)-claims["iat"].(float64) != 3600 {
		t.Fatalf("claims = %v, %v", claims, err)
	}

	h := &Handler{}
	foreign, _ := json.Marshal(map[string]string{"type": "service_account", "client_email": "x", "private_key": account.PrivateKey, "token_uri": "https://evil.example/token"})
	if _, err := h.vertexAccessToken(context.Background(), "integration-1", string(foreign)); err == nil {
		t.Fatal("a foreign token_uri was accepted")
	}
}
