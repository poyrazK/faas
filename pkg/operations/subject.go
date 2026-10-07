// ADR-639: business references are immutable correlation metadata, not ownership.
package operations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

func validateSubjectSpec(spec api.OperationSubjectSpec) error {
	if err := api.ValidateOperationSubjectType(spec.Type); err != nil {
		return err
	}
	_, err := subjectPointer(spec.IDFrom)
	return err
}

func subjectPointer(pointer string) ([]string, error) {
	return operationJSONPointer(pointer, "operation subject id_from")
}

func operationJSONPointer(pointer, label string) ([]string, error) {
	if len(pointer) == 0 || len(pointer) > api.OperationPathMaxBytes || !utf8.ValidString(pointer) || !strings.HasPrefix(pointer, "/") || strings.ContainsFunc(pointer, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return nil, fmt.Errorf("%s must be a bounded JSON Pointer starting with /", label)
	}
	parts := strings.Split(pointer[1:], "/")
	for i, part := range parts {
		var decoded strings.Builder
		for j := 0; j < len(part); j++ {
			if part[j] != '~' {
				decoded.WriteByte(part[j])
				continue
			}
			j++
			if j >= len(part) || (part[j] != '0' && part[j] != '1') {
				return nil, fmt.Errorf("%s has an invalid JSON Pointer escape", label)
			}
			if part[j] == '0' {
				decoded.WriteByte('~')
			} else {
				decoded.WriteByte('/')
			}
		}
		parts[i] = decoded.String()
	}
	return parts, nil
}

// ValidateOperationWorkflowInstancePointer checks an app-declared JSON Pointer
// without requiring a milestone payload at deployment time.
func ValidateOperationWorkflowInstancePointer(pointer string) error {
	_, err := operationJSONPointer(pointer, "operation workflow instance_id_from")
	return err
}

// ExtractOperationWorkflowInstanceID selects the stable application key from
// a schema-validated, retained milestone payload.
func ExtractOperationWorkflowInstanceID(pointer string, payload []byte) (string, error) {
	parts, err := operationJSONPointer(pointer, "operation workflow instance_id_from")
	if err != nil {
		return "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	value, err := readJSONValue(decoder, 0)
	if err != nil {
		return "", fmt.Errorf("operation workflow instance ID payload is invalid")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return "", fmt.Errorf("operation workflow instance ID payload is invalid")
	}
	for _, part := range parts {
		switch container := value.(type) {
		case map[string]any:
			value = container[part]
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(container) || strconv.Itoa(index) != part {
				return "", fmt.Errorf("operation workflow instance_id_from does not resolve to a string")
			}
			value = container[index]
		default:
			return "", fmt.Errorf("operation workflow instance_id_from does not resolve to a string")
		}
	}
	instanceID, ok := value.(string)
	if !ok || api.ValidateOperationWorkflowInstanceID(instanceID) != nil {
		return "", fmt.Errorf("operation workflow instance_id_from does not resolve to a valid string")
	}
	return instanceID, nil
}

// ExtractOperationSubject runs only for a new admission, after input validation.
// An idempotent replay returns its original reference even after a redeploy.
func ExtractOperationSubject(spec *api.OperationSubjectSpec, input []byte) (*api.OperationSubject, error) {
	if spec == nil {
		return nil, nil
	}
	if err := validateSubjectSpec(*spec); err != nil {
		return nil, err
	}
	parts, _ := subjectPointer(spec.IDFrom)
	var value any
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("operation subject input is invalid JSON")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("operation subject input is invalid JSON")
	}
	for _, part := range parts {
		switch container := value.(type) {
		case map[string]any:
			value = container[part]
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(container) || strconv.Itoa(index) != part {
				return nil, fmt.Errorf("operation subject id_from does not resolve to a string")
			}
			value = container[index]
		default:
			return nil, fmt.Errorf("operation subject id_from does not resolve to a string")
		}
	}
	id, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("operation subject id_from does not resolve to a string")
	}
	subject := &api.OperationSubject{Type: spec.Type, ID: id}
	if err := api.ValidateOperationSubject(*subject); err != nil {
		return nil, err
	}
	return subject, nil
}
