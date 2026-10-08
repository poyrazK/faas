package api

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// OperationSubjectSpec explicitly selects business correlation metadata from input.
// The reference grants no authority over the application-owned business entity.
type OperationSubjectSpec struct {
	Type   string `json:"type" yaml:"type" toml:"type"`
	IDFrom string `json:"id_from" yaml:"id_from" toml:"id_from"`
}

// OperationSubject is captured once at admission and survives execution recovery.
type OperationSubject struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

var operationSubjectType = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func ValidateOperationSubjectType(value string) error {
	if len(value) == 0 || len(value) > OperationNameMaxBytes || !operationSubjectType.MatchString(value) {
		return fmt.Errorf("operation subject type must be a bounded lowercase slug")
	}
	return nil
}

func ValidateOperationSubject(subject OperationSubject) error {
	if err := ValidateOperationSubjectType(subject.Type); err != nil {
		return err
	}
	if len(subject.ID) == 0 || len(subject.ID) > OperationSubjectIDMaxBytes || !utf8.ValidString(subject.ID) || strings.ContainsFunc(subject.ID, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return fmt.Errorf("operation subject id must be a nonempty bounded UTF-8 string without control characters")
	}
	return nil
}
