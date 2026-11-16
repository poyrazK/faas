package internal

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/functionschema"
)

// Compiled runtimes (Go, binary) have no in-language adapter, so the runner
// checks typed-client calls against FAAS_FUNCTION_INPUT_SCHEMA itself, with
// the same subset and the same 422 as the Node and Python adapters
// (ADR-20261112160000 §5).

// functionInputBodyMax bounds the body read for the check; the gateway caps
// request bodies well below it.
const functionInputBodyMax = 64 << 20

var functionInputCache struct {
	sync.Mutex
	raw    string
	schema functionschema.Schema
}

func functionInputSchema() functionschema.Schema {
	raw := os.Getenv(api.FunctionInputSchemaEnv)
	functionInputCache.Lock()
	defer functionInputCache.Unlock()
	if raw == functionInputCache.raw {
		return functionInputCache.schema
	}
	functionInputCache.raw, functionInputCache.schema = raw, nil
	if raw == "" {
		return nil
	}
	schema, err := functionschema.Parse([]byte(raw))
	if err != nil {
		log.Printf("gregale: ignoring %s that is not a JSON object: %v", api.FunctionInputSchemaEnv, err)
		return nil
	}
	functionInputCache.schema = schema
	return schema
}

// RejectInvalidFunctionInput answers 422 function_input_invalid and reports
// true when r is a typed-client call whose body breaks the function's input
// schema. Otherwise r's body is left readable and it reports false.
func RejectInvalidFunctionInput(w http.ResponseWriter, r *http.Request) bool {
	schema := functionInputSchema()
	if schema == nil || r.Header.Get(api.FunctionCallHeader) != "1" {
		return false
	}
	var body []byte
	if r.Body != nil {
		var err error
		body, err = io.ReadAll(io.LimitReader(r.Body, functionInputBodyMax))
		_ = r.Body.Close()
		if err != nil {
			body = nil
		}
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	detail := schema.CheckBody(body)
	if detail == "" {
		return false
	}
	docs := "https://gregale.dev/docs/errors#" + api.CodeFunctionInputInvalid
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type": docs, "title": "Function input invalid", "status": http.StatusUnprocessableEntity,
		"code": api.CodeFunctionInputInvalid, "detail": detail, "docs_url": docs,
	})
	return true
}
