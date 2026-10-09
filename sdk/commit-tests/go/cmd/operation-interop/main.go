package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	faas "github.com/poyrazK/faas/sdk/go"
)

func run() error {
	var fixture struct {
		Headers map[string]string `json:"headers"`
		Method  string            `json:"method"`
		Path    string            `json:"path"`
		Body    string            `json:"body_base64"`
	}
	raw, err := os.ReadFile(os.Getenv("OPERATION_REQUEST_FILE"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		return err
	}
	body, err := base64.StdEncoding.DecodeString(fixture.Body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(fixture.Method, "http://managed-app"+fixture.Path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	for name, value := range fixture.Headers {
		req.Header.Set(name, value)
	}
	cfg, err := pgx.ParseConfig(os.Getenv("OPERATION_CROSS_DATABASE_URL"))
	if err != nil {
		return err
	}
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var result faas.OperationTransactionResult
	if req.Header.Get("X-Gregale-Customer-Operation-Receipt-Version") != "" {
		input, parseErr := faas.CustomerOperationRequestFromHTTP(req, body)
		if parseErr != nil {
			return parseErr
		}
		result, err = faas.WithCustomerOperationTransaction(ctx, db, input, func(tx faas.OperationSQLTransaction) (json.RawMessage, error) {
			if os.Getenv("OPERATION_MODE") != "write" {
				return nil, fmt.Errorf("cross SDK receipt callback reran")
			}
			_, err := tx.ExecContext(ctx, "UPDATE business.counter SET total=total+1 WHERE id=1")
			return json.RawMessage(`{"file":"ready.csv","value":9007199254740993,"wide":1e400,"label":"π <>&"}`), err
		})
	} else {
		input, parseErr := faas.OperationRequestFromHTTP(req, body)
		if parseErr != nil {
			return parseErr
		}
		result, err = faas.WithOperationTransaction(ctx, db, input, func(tx faas.OperationSQLTransaction) (faas.OperationOutcome, error) {
			if os.Getenv("OPERATION_MODE") != "write" {
				return faas.OperationOutcome{}, fmt.Errorf("cross SDK receipt callback reran")
			}
			_, err := tx.ExecContext(ctx, "UPDATE business.counter SET total=total+1 WHERE id=1")
			// Valid JSON whose wire size and numbers cannot be reconstructed from a
			// JavaScript/Python float. The saved payload remains below 64 KiB.
			payload := `{"wide":1e400,"integer":1` + strings.Repeat("0", 5000) + `,"values":[` + strings.Repeat("1e1,", 14990) + "1e1]}"
			return faas.OperationOutcome{Result: json.RawMessage(`{"value":9007199254740993,"label":"π <>&"}`), Effects: []faas.ManagedOperationEffect{{Name: "notify", WebhookID: "cccbbbaa-3333-4333-8333-cccccccccccc", Type: "order.fulfilled", Payload: json.RawMessage(payload)}}}, err
		})
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Body     string `json:"body"`
		Replayed bool   `json:"replayed"`
	}{string(result.Body), result.Replayed})
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
