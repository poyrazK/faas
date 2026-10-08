package faas

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// OperationReceiptSchema is customer-owned DDL. Install explicitly as the
// database owner; WithOperationTransaction never installs or prunes receipts.
//
//go:embed operation_schema.sql
var OperationReceiptSchema string

// CustomerOperationReceiptSchema is the application-owned Customer Operations
// schema. Install and retain it explicitly as the database owner.
//
//go:embed customer_operation_schema.sql
var CustomerOperationReceiptSchema string

var (
	ErrInvalidOperationRequest  = errors.New("invalid managed operation request")
	ErrOperationReceiptConflict = errors.New("operation receipt scope or input differs")
	ErrOperationCommitUnknown   = errors.New("operation commit outcome unknown; retry with the same operation identity")
)

// OperationRequest contains platform-authored scope and the original HTTP input.
// Generation is validated but deliberately excluded from deduplication identity.
type OperationRequest struct {
	managed          bool
	receiptBinding   string
	OperationID      string
	AccountID        string
	AppID            string
	PlatformTenantID string
	Generation       int64
	Method           string
	Path             string
	Body             []byte
}

type OperationOutcome struct {
	Result  json.RawMessage
	Effects []ManagedOperationEffect
}

type OperationTransactionResult struct {
	// Send Body unchanged as application/json after this function succeeds.
	Body     json.RawMessage
	Replayed bool
}

// OperationSQLTransaction excludes commit/rollback: the wrapper owns them.
type OperationSQLTransaction interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// OperationRequestFromHTTP accepts headers only behind Gregale ingress, which
// strips customer values and authors the operation identity. Supply the original
// body bytes before decoding them. This does not authenticate arbitrary servers.
func OperationRequestFromHTTP(r *http.Request, body []byte) (OperationRequest, error) {
	if r == nil || r.URL == nil {
		return OperationRequest{}, ErrInvalidOperationRequest
	}
	read := func(name string, optional bool) (string, error) {
		var values []string
		for key, list := range r.Header {
			if strings.EqualFold(key, name) {
				values = append(values, list...)
			}
		}
		if optional && len(values) == 0 {
			return "", nil
		}
		if len(values) != 1 || values[0] == "" {
			return "", ErrInvalidOperationRequest
		}
		return values[0], nil
	}
	version, err := read("X-Gregale-Operation-Result-Version", false)
	if err != nil || version != "1" {
		return OperationRequest{}, ErrInvalidOperationRequest
	}
	values := make([]string, 5)
	for i, name := range []string{"X-Gregale-Operation-Id", "X-Faas-Tenant-Id", "X-Faas-App-Id", "X-Faas-Platform-Tenant-Id", "X-Gregale-Operation-Generation"} {
		values[i], err = read(name, i == 3)
		if err != nil {
			return OperationRequest{}, err
		}
	}
	generation, err := strconv.ParseInt(values[4], 10, 64)
	if err != nil || strconv.FormatInt(generation, 10) != values[4] {
		return OperationRequest{}, ErrInvalidOperationRequest
	}
	return normalizeOperationRequest(OperationRequest{managed: true, OperationID: values[0], AccountID: values[1], AppID: values[2], PlatformTenantID: values[3], Generation: generation, Method: r.Method, Path: r.URL.RequestURI(), Body: body})
}

func normalizeOperationRequest(request OperationRequest) (OperationRequest, error) {
	if !request.managed {
		return request, ErrInvalidOperationRequest
	}
	if request.receiptBinding != "" && (!regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(request.receiptBinding) || request.PlatformTenantID == "") {
		return request, ErrInvalidOperationRequest
	}
	request.OperationID, request.AccountID, request.AppID, request.PlatformTenantID = strings.ToLower(request.OperationID), strings.ToLower(request.AccountID), strings.ToLower(request.AppID), strings.ToLower(request.PlatformTenantID)
	for _, id := range []string{request.OperationID, request.AccountID, request.AppID} {
		if !operationUUID(id) {
			return request, ErrInvalidOperationRequest
		}
	}
	if request.PlatformTenantID != "" && !operationUUID(request.PlatformTenantID) {
		return request, ErrInvalidOperationRequest
	}
	if request.Generation <= 0 || len(request.Method) == 0 || len(request.Method) > operationIdentityBytes {
		return request, ErrInvalidOperationRequest
	}
	for _, char := range request.Method {
		if char < 'A' || char > 'Z' {
			return request, ErrInvalidOperationRequest
		}
	}
	if !strings.HasPrefix(request.Path, "/") || !utf8.ValidString(request.Path) || strings.ContainsAny(request.Path, "\r\n\x00") || len(request.Method)+len(request.Path)+len(request.Body) > operationRequestBytes {
		return request, ErrInvalidOperationRequest
	}
	request.Body = bytes.Clone(request.Body)
	return request, nil
}

func operationUUID(value string) bool {
	return commitUUID(value) && value != "00000000-0000-0000-0000-000000000000"
}

// OperationRequestDigest uses the shared SDK v1 method/path/raw-body framing.
func OperationRequestDigest(input OperationRequest) ([]byte, error) {
	request, err := normalizeOperationRequest(input)
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	prefix := "gregale-operation-request-v1\n"
	if request.receiptBinding != "" {
		prefix = "gregale-customer-operation-request-v1\n" + request.receiptBinding + "\n"
	}
	_, _ = hash.Write([]byte(prefix + request.Method + "\n" + request.Path + "\n"))
	_, _ = hash.Write(request.Body)
	return hash.Sum(nil), nil
}

func encodeOperationOutcome(outcome OperationOutcome) ([]byte, error) {
	if outcome.Effects == nil {
		outcome.Effects = []ManagedOperationEffect{}
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(ManagedOperationResult{Version: 1, Result: outcome.Result, Effects: outcome.Effects}); err != nil {
		return nil, fmt.Errorf("operation response encode: %w", err)
	}
	body := bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))
	return body, validateOperationResponse(body)
}

func validateOperationResponse(body []byte) error {
	if len(body) > operationResponseBytes || !utf8.Valid(body) {
		return errors.New("invalid or oversized operation response")
	}
	var envelope ManagedOperationResult
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil || !json.Valid(body) || envelope.Version != 1 || len(envelope.Result) == 0 || envelope.Effects == nil || len(envelope.Effects) > operationEffects {
		return errors.New("invalid saved operation response")
	}
	names := map[string]bool{}
	for _, effect := range envelope.Effects {
		if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`).MatchString(effect.Name) || names[effect.Name] || !operationUUID(strings.ToLower(effect.WebhookID)) || len(effect.Type) > operationTypeBytes || !regexp.MustCompile(`^[a-z][a-z0-9_.-]*$`).MatchString(effect.Type) || len(effect.Payload) > operationPayloadBytes || !json.Valid(effect.Payload) {
			return errors.New("invalid operation effect")
		}
		names[effect.Name] = true
	}
	return nil
}

// WithOperationTransaction owns a fresh READ COMMITTED transaction. The handler
// must use only this transaction for business writes, must not control its
// lifecycle, and must not perform external side effects. A later generation
// replays the exact saved response without repeating committed business writes.
func WithOperationTransaction(ctx context.Context, db *sql.DB, input OperationRequest, handler func(OperationSQLTransaction) (OperationOutcome, error)) (OperationTransactionResult, error) {
	if input.receiptBinding != "" {
		return OperationTransactionResult{}, ErrInvalidOperationRequest
	}
	return withOperationTransaction(ctx, db, input, handler)
}

func withOperationTransaction(ctx context.Context, db *sql.DB, input OperationRequest, handler func(OperationSQLTransaction) (OperationOutcome, error)) (OperationTransactionResult, error) {
	request, err := normalizeOperationRequest(input)
	if err != nil {
		return OperationTransactionResult{}, err
	}
	if db == nil || handler == nil {
		return OperationTransactionResult{}, ErrInvalidOperationRequest
	}
	digest, err := OperationRequestDigest(request)
	if err != nil {
		return OperationTransactionResult{}, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return OperationTransactionResult{}, fmt.Errorf("operation transaction begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit or errors is best effort
	receiptTable := "public.gregale_operation_inbox"
	lockNamespace := "gregale.operation-inbox.v1:"
	if request.receiptBinding != "" {
		receiptTable = "public.gregale_customer_operation_inbox"
		lockNamespace = "gregale.customer-operation-inbox.v1:"
	}
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1 || $2::uuid::text, 0))", lockNamespace, request.OperationID); err != nil {
		return OperationTransactionResult{}, fmt.Errorf("operation transaction lock: %w", err)
	}
	var account, app, tenant, body string
	var storedDigest []byte
	readReceipt := "SELECT account_id::text,app_id::text,coalesce(platform_tenant_id::text,'') AS platform_tenant_id,request_digest,response_body FROM " + receiptTable + " WHERE operation_id=$1::uuid"
	err = tx.QueryRowContext(ctx, readReceipt, request.OperationID).Scan(&account, &app, &tenant, &storedDigest, &body)
	replayed := err == nil
	if replayed {
		if account != request.AccountID || app != request.AppID || tenant != request.PlatformTenantID || !bytes.Equal(digest, storedDigest) {
			return OperationTransactionResult{}, ErrOperationReceiptConflict
		}
		if err := validateOperationResponse([]byte(body)); err != nil {
			return OperationTransactionResult{}, err
		}
		if request.receiptBinding != "" {
			if _, err := customerOperationResult([]byte(body)); err != nil {
				return OperationTransactionResult{}, err
			}
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		outcome, err := handler(tx)
		if err != nil {
			return OperationTransactionResult{}, err
		}
		encoded, err := encodeOperationOutcome(outcome)
		if err != nil {
			return OperationTransactionResult{}, err
		}
		body = string(encoded)
		insertReceipt := "INSERT INTO " + receiptTable + "(operation_id,account_id,app_id,platform_tenant_id,request_digest,response_body) VALUES ($1::uuid,$2::uuid,$3::uuid,nullif($4,'')::uuid,$5,$6)"
		if _, err := tx.ExecContext(ctx, insertReceipt, request.OperationID, request.AccountID, request.AppID, request.PlatformTenantID, digest, body); err != nil {
			return OperationTransactionResult{}, fmt.Errorf("operation receipt insert: %w", err)
		}
	} else {
		return OperationTransactionResult{}, fmt.Errorf("operation receipt read: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return OperationTransactionResult{}, fmt.Errorf("%w: %w", ErrOperationCommitUnknown, err)
	}
	return OperationTransactionResult{Body: json.RawMessage(body), Replayed: replayed}, nil
}
