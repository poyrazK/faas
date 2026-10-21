package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type dataAPIShapeFilter struct {
	Column   string `json:"column"`
	Operator string `json:"operator"`
}
type dataAPIShapeOrder struct {
	Column     string `json:"column"`
	Ascending  bool   `json:"ascending"`
	NullsFirst bool   `json:"nulls_first"`
}
type dataAPIQueryShape struct {
	Projection []string             `json:"projection"`
	Filters    []dataAPIShapeFilter `json:"filters"`
	Order      []dataAPIShapeOrder  `json:"order"`
	Complex    bool                 `json:"complex"`
	Limit      bool                 `json:"limit"`
	Offset     bool                 `json:"offset"`
	Count      string               `json:"count"`
}
type dataAPIShapeStats struct {
	Operation   string              `json:"operation"`
	Kind        string              `json:"operation_kind"`
	Method      string              `json:"method"`
	ShapeID     string              `json:"query_shape_id"`
	Fingerprint string              `json:"schema_fingerprint"`
	Shape       *dataAPIQueryShape  `json:"query_shape"`
	Stats       dataAPIRequestStats `json:"stats"`
	RequestIDs  []string            `json:"sample_request_ids"`
}

var dataAPIShapeName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)
var dataAPIShapeHash = regexp.MustCompile(`^[a-f0-9]{64}$`)

func validateDataAPIQueryShape(record dataAPIRequestRecord) error {
	if record.Shape == nil && record.Operation == "" && record.OperationKind == "" && record.ShapeID == "" && record.Fingerprint == "" {
		return nil
	}
	bad := errors.New("invalid query-shape metadata")
	if record.Shape == nil || !dataAPIShapeHash.MatchString(record.ShapeID) || !dataAPIShapeHash.MatchString(record.Fingerprint) || !dataAPIDiagnosticAllowed(record.OperationKind, "relation", "rpc") || !dataAPIDiagnosticAllowed(record.Route, "rest", "rpc") || record.Outcome == "authentication_failed" {
		return bad
	}
	parts := strings.Split(record.Operation, ".")
	if len(parts) != 2 || !dataAPIShapeName.MatchString(parts[0]) || !dataAPIShapeName.MatchString(parts[1]) {
		return bad
	}
	if (record.Route == "rpc") != (record.OperationKind == "rpc") {
		return bad
	}
	shape := record.Shape
	if len(shape.Projection) == 0 || len(shape.Projection) > 64 || len(shape.Filters) > 64 || len(shape.Order) > 16 || !dataAPIDiagnosticAllowed(shape.Count, "none", "exact", "planned", "estimated") {
		return bad
	}
	for _, column := range shape.Projection {
		if column != "*" && !dataAPIShapeName.MatchString(column) {
			return bad
		}
	}
	for _, filter := range shape.Filters {
		if !dataAPIShapeName.MatchString(filter.Column) || !dataAPIDiagnosticAllowed(strings.TrimPrefix(filter.Operator, "not."), "eq", "neq", "gt", "gte", "lt", "lte", "like", "ilike", "in", "is", "cs", "cd", "ov", "sl", "sr", "nxl", "nxr", "adj", "fts", "plfts", "phfts", "wfts", "unknown") {
			return bad
		}
	}
	for _, order := range shape.Order {
		if !dataAPIShapeName.MatchString(order.Column) {
			return bad
		}
	}
	raw, err := json.Marshal([]any{record.Method, record.Operation, record.OperationKind, shape})
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != record.ShapeID {
		return bad
	}
	return nil
}

type dataAPIOperationStats struct {
	Operation   string              `json:"operation"`
	Kind        string              `json:"operation_kind"`
	Fingerprint string              `json:"schema_fingerprint"`
	Stats       dataAPIRequestStats `json:"stats"`
	ShapeCount  int                 `json:"query_shapes"`
}
