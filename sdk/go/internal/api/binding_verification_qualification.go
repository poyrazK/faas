package api

import "time"

// Only successful completion responses may declare pending qualification.
// Observations grant no admission or release authority.
type BindingVerificationQualification struct {
	CheckedAt  time.Time                                   `json:"checked_at"`
	Conditions []BindingVerificationQualificationCondition `json:"conditions"`
}
type BindingVerificationQualificationCondition struct {
	App     string `json:"app"`
	Code    string `json:"code"`
	Type    string `json:"type,omitempty"`
	Name    string `json:"name,omitempty"`
	Binding string `json:"binding,omitempty"`
	Scope   string `json:"scope,omitempty"`
}
