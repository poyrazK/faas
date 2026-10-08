package faas

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	operationapi "github.com/poyrazK/faas/sdk/go/internal/api"
)

const (
	customerOperationIdentityTokenBytes = 8192
	customerOperationIdentityBodyBytes  = 16384
)

var (
	customerOperationAttemptPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
	customerOperationCapability     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	customerOperationMethod         = regexp.MustCompile(`^[A-Z]+$`)
	customerOperationUUIDPattern    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// CustomerOperationRuntimeOptions configures the trusted application-side
// Customer Operation transaction helper.
type CustomerOperationRuntimeOptions struct {
	APIURL           string
	IdentityEndpoint string
	HTTPClient       *http.Client
	Timeout          time.Duration
}

// CustomerOperationRuntime validates and publishes transaction-backed
// customer facts using a fresh workload identity token for each API call.
type CustomerOperationRuntime struct {
	apiOrigin   string
	identityURL *url.URL
	httpClient  *http.Client
	timeout     time.Duration
}

type customerOperationInput struct {
	operationID       string
	accountID         string
	appID             string
	platformTenantID  string
	resultMaxBytes    int64
	milestonesEnabled bool
	method            string
	path              string
	body              []byte
	proof             operationapi.OperationRuntimeProof
}

// NewCustomerOperationRuntime constructs a runtime integration without making
// network requests. API calls fetch a current audience-bound workload token.
func NewCustomerOperationRuntime(options CustomerOperationRuntimeOptions) (*CustomerOperationRuntime, error) {
	apiURL, err := url.Parse(options.APIURL)
	if err != nil || apiURL.Scheme == "" || apiURL.Hostname() == "" || apiURL.User != nil || apiURL.RawQuery != "" || apiURL.ForceQuery || apiURL.Fragment != "" {
		return nil, errors.New("faas: Customer Operations API URL is invalid")
	}
	if apiURL.Scheme != "https" && !(apiURL.Scheme == "http" && customerOperationLoopback(apiURL.Hostname())) {
		return nil, errors.New("faas: Customer Operations API requires HTTPS or loopback HTTP")
	}
	apiOrigin := (&url.URL{Scheme: apiURL.Scheme, Host: apiURL.Host}).String()

	identityEndpoint := options.IdentityEndpoint
	if identityEndpoint == "" {
		identityEndpoint = os.Getenv("FAAS_WORKLOAD_IDENTITY_ENDPOINT")
	}
	identityURL, err := url.Parse(identityEndpoint)
	if err != nil || identityURL.Scheme != "http" || identityURL.Hostname() == "" || identityURL.User != nil || identityURL.Fragment != "" {
		return nil, errors.New("faas: workload identity endpoint is invalid")
	}
	if !customerOperationLoopback(identityURL.Hostname()) {
		return nil, errors.New("faas: workload identity endpoint must use loopback HTTP")
	}
	query := identityURL.Query()
	query.Set("audience", "gregale:operations")
	identityURL.RawQuery = query.Encode()

	timeout := options.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	if timeout <= 0 || timeout > 10*time.Second {
		return nil, errors.New("faas: invalid Customer Operations timeout")
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	clientCopy := *httpClient
	clientCopy.Timeout = timeout
	clientCopy.Jar = nil
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &CustomerOperationRuntime{apiOrigin: apiOrigin, identityURL: identityURL, httpClient: &clientCopy, timeout: timeout}, nil
}

func customerOperationLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func customerHeaderValues(header http.Header, name string) []string {
	values := make([]string, 0, 1)
	for key, entries := range header {
		if strings.EqualFold(key, name) {
			values = append(values, entries...)
		}
	}
	return values
}

func customerHeader(header http.Header, name string, optional bool) (string, error) {
	values := customerHeaderValues(header, name)
	if optional && len(values) == 0 {
		return "", nil
	}
	if len(values) != 1 || values[0] == "" {
		return "", ErrInvalidCustomerOperationRequest
	}
	return values[0], nil
}

func customerOperationUUID(value string) (string, error) {
	value = strings.ToLower(value)
	if !customerOperationUUIDPattern.MatchString(value) || value == "00000000-0000-0000-0000-000000000000" {
		return "", ErrInvalidCustomerOperationRequest
	}
	return value, nil
}

func customerOperationInputFromHTTP(request *http.Request, originalBody []byte) (customerOperationInput, error) {
	if request == nil || request.URL == nil {
		return customerOperationInput{}, ErrInvalidCustomerOperationRequest
	}
	allowedOperationHeaders := map[string]bool{
		"x-gregale-operation-attempt": true,
		"x-gregale-operation-capability": true,
	}
	allowedCustomerHeaders := map[string]bool{
		"x-gregale-customer-operation-id": true,
		"x-gregale-customer-operation-transaction-version": true,
		"x-gregale-customer-operation-result-max-bytes": true,
		"x-gregale-customer-operation-milestone-version": true,
	}
	for key := range request.Header {
		name := strings.ToLower(key)
		if strings.HasPrefix(name, "x-gregale-operation-") && !allowedOperationHeaders[name] ||
			strings.HasPrefix(name, "x-gregale-customer-operation-") && !allowedCustomerHeaders[name] {
			return customerOperationInput{}, ErrInvalidCustomerOperationRequest
		}
	}

	transactionVersion, err := customerHeader(request.Header, "X-Gregale-Customer-Operation-Transaction-Version", false)
	if err != nil || transactionVersion != "1" {
		return customerOperationInput{}, ErrInvalidCustomerOperationRequest
	}
	milestoneVersion, err := customerHeader(request.Header, "X-Gregale-Customer-Operation-Milestone-Version", true)
	if err != nil || milestoneVersion != "" && milestoneVersion != "1" {
		return customerOperationInput{}, ErrInvalidCustomerOperationRequest
	}
	operationID, err := customerHeader(request.Header, "X-Gregale-Customer-Operation-Id", false)
	if err != nil {
		return customerOperationInput{}, err
	}
	operationID, err = customerOperationUUID(operationID)
	if err != nil {
		return customerOperationInput{}, err
	}
	accountID, err := customerHeader(request.Header, "X-Faas-Tenant-Id", false)
	if err != nil {
		return customerOperationInput{}, err
	}
	accountID, err = customerOperationUUID(accountID)
	if err != nil {
		return customerOperationInput{}, err
	}
	appID, err := customerHeader(request.Header, "X-Faas-App-Id", false)
	if err != nil {
		return customerOperationInput{}, err
	}
	appID, err = customerOperationUUID(appID)
	if err != nil {
		return customerOperationInput{}, err
	}
	platformTenantID, err := customerHeader(request.Header, "X-Faas-Platform-Tenant-Id", false)
	if err != nil {
		return customerOperationInput{}, err
	}
	platformTenantID, err = customerOperationUUID(platformTenantID)
	if err != nil {
		return customerOperationInput{}, err
	}

	resultMaxRaw, err := customerHeader(request.Header, "X-Gregale-Customer-Operation-Result-Max-Bytes", false)
	if err != nil || !customerOperationAttemptPattern.MatchString(resultMaxRaw) {
		return customerOperationInput{}, ErrInvalidCustomerOperationRequest
	}
	resultMaxBytes, err := strconv.ParseInt(resultMaxRaw, 10, 64)
	if err != nil || resultMaxBytes <= 0 || resultMaxBytes > operationResponseBytes || strconv.FormatInt(resultMaxBytes, 10) != resultMaxRaw {
		return customerOperationInput{}, ErrInvalidCustomerOperationRequest
	}

	invocationID, err := customerHeader(request.Header, "X-Faas-Invocation-Id", false)
	if err != nil {
		return customerOperationInput{}, err
	}
	invocationID, err = customerOperationUUID(invocationID)
	if err != nil {
		return customerOperationInput{}, err
	}
	attemptRaw, err := customerHeader(request.Header, "X-Gregale-Operation-Attempt", false)
	if err != nil || !customerOperationAttemptPattern.MatchString(attemptRaw) {
		return customerOperationInput{}, ErrInvalidCustomerOperationRequest
	}
	attempt, err := strconv.ParseInt(attemptRaw, 10, 64)
	if err != nil || attempt > 9007199254740991 || int64(int(attempt)) != attempt || strconv.FormatInt(attempt, 10) != attemptRaw {
		return customerOperationInput{}, ErrInvalidCustomerOperationRequest
	}
	capability, err := customerHeader(request.Header, "X-Gregale-Operation-Capability", false)
	if err != nil || !customerOperationCapability.MatchString(capability) {
		return customerOperationInput{}, ErrInvalidCustomerOperationRequest
	}
	if !customerOperationMethod.MatchString(request.Method) || len(request.Method) > operationIdentityBytes || request.RequestURI == "" ||
		!strings.HasPrefix(request.RequestURI, "/") || !utf8.ValidString(request.RequestURI) || strings.ContainsAny(request.RequestURI, "\r\n\x00") ||
		len(request.Method)+len(request.RequestURI)+len(originalBody) > operationRequestBytes {
		return customerOperationInput{}, ErrInvalidCustomerOperationRequest
	}
	return customerOperationInput{
		operationID: operationID, accountID: accountID, appID: appID, platformTenantID: platformTenantID,
		resultMaxBytes: resultMaxBytes, milestonesEnabled: milestoneVersion == "1", method: request.Method,
		path: request.RequestURI, body: append([]byte(nil), originalBody...),
		proof: operationapi.OperationRuntimeProof{InvocationID: invocationID, Attempt: int(attempt), Capability: capability},
	}, nil
}

func (r *CustomerOperationRuntime) operationClient(ctx context.Context) (*operationapi.Client, error) {
	identityRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, r.identityURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("faas: create workload identity request: %w", err)
	}
	identityRequest.Header.Set("Accept", "application/json")
	identityRequest.Header.Set("Cache-Control", "no-store")
	response, err := r.httpClient.Do(identityRequest)
	if err != nil {
		return nil, fmt.Errorf("faas: read workload identity: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, customerOperationIdentityBodyBytes+1))
	if err != nil || len(data) > customerOperationIdentityBodyBytes {
		return nil, errors.New("faas: workload identity response is invalid or oversized")
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("faas: workload identity returned HTTP %d", response.StatusCode)
	}
	var credentials struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(data, &credentials); err != nil || credentials.AccessToken == "" || len(credentials.AccessToken) > customerOperationIdentityTokenBytes || strings.ContainsAny(credentials.AccessToken, "\r\n \t") {
		return nil, errors.New("faas: invalid workload identity token")
	}
	client := operationapi.NewClient(r.apiOrigin, credentials.AccessToken)
	apiHTTPClient := client.HTTPClient()
	apiHTTPClient.Timeout = r.timeout
	apiHTTPClient.Transport = r.httpClient.Transport
	apiHTTPClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client, nil
}

func (r *CustomerOperationRuntime) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, r.timeout)
}
