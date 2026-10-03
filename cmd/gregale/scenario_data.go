package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

const testDataCaseLimit = 100

type testDataCase struct {
	Name   string
	Values map[string]any
}

// Case data stays out of receipts. Reports identify rows without copying
// credentials or application inputs from the data file.
func readTestData(path string) ([]testDataCase, []string, error) {
	if path == "" {
		return []testDataCase{{}}, nil, nil
	}
	file, err := openCustomerFile(path)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, testHTTPBodyLimit+1))
	if err != nil {
		return nil, nil, err
	}
	if len(body) > testHTTPBodyLimit {
		return nil, nil, errors.New("case data exceeds 1 MiB")
	}
	var rows []map[string]any
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		if err := decoder.Decode(&rows); err != nil {
			return nil, nil, fmt.Errorf("case data must be a JSON array of objects: %w", err)
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			return nil, nil, errors.New("case data must contain exactly one JSON document")
		}
	case ".csv":
		rows, err = readTestCSVData(body)
		if err != nil {
			return nil, nil, err
		}
	default:
		return nil, nil, errors.New("case data must use a .json or .csv file")
	}
	if len(rows) == 0 || len(rows) > testDataCaseLimit {
		return nil, nil, fmt.Errorf("case data must contain between 1 and %d rows", testDataCaseLimit)
	}
	fields := make([]string, 0, len(rows[0]))
	for key := range rows[0] {
		if !testTriggerKeyPattern.MatchString(key) {
			return nil, nil, fmt.Errorf("invalid case data field %q; use lowercase letters, digits, and underscores", key)
		}
		fields = append(fields, key)
	}
	if len(fields) == 0 || len(fields) > 32 {
		return nil, nil, errors.New("case data must declare between 1 and 32 fields")
	}
	sort.Strings(fields)
	cases := make([]testDataCase, 0, len(rows))
	for i, row := range rows {
		if len(row) != len(fields) {
			return nil, nil, fmt.Errorf("case data row %d must contain the same fields as row 1", i+1)
		}
		for _, field := range fields {
			value, exists := row[field]
			if !exists {
				return nil, nil, fmt.Errorf("case data row %d is missing field %q", i+1, field)
			}
			switch value.(type) {
			case string, json.Number, bool:
			default:
				return nil, nil, fmt.Errorf("case data row %d field %q must be a string, number, or boolean", i+1, field)
			}
		}
		cases = append(cases, testDataCase{Name: fmt.Sprintf("row-%d", i+1), Values: row})
	}
	return cases, fields, nil
}

func readTestCSVData(body []byte) ([]map[string]any, error) {
	reader := csv.NewReader(bytes.NewReader(body))
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read case data CSV header: %w", err)
	}
	if len(header) > 32 {
		return nil, errors.New("case data must declare between 1 and 32 fields")
	}
	seen := make(map[string]bool, len(header))
	for _, field := range header {
		if !testTriggerKeyPattern.MatchString(field) || seen[field] {
			return nil, fmt.Errorf("CSV header has invalid or duplicate field %q", field)
		}
		seen[field] = true
	}
	rows := make([]map[string]any, 0)
	for {
		values, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read case data CSV row %d: %w", len(rows)+1, err)
		}
		row := make(map[string]any, len(header))
		for i, field := range header {
			row[field] = values[i]
		}
		rows = append(rows, row)
		if len(rows) > testDataCaseLimit {
			return nil, fmt.Errorf("case data must contain at most %d rows", testDataCaseLimit)
		}
	}
	return rows, nil
}
