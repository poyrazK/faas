package mcpmanagement

type diagnosticReadError struct {
	Code              string `json:"code"`
	Status            int    `json:"status,omitempty"`
	RetryAfterSeconds *int64 `json:"retry_after_seconds,omitempty"`
}

// diagnosticReadError releases stable retry metadata without native error prose.
func (m *management) diagnosticReadError(err error) *diagnosticReadError {
	detail := m.toolError(err).StructuredContent.(ToolError)
	if detail.Code == "" {
		detail.Code = "request_failed"
	}
	result := &diagnosticReadError{Code: detail.Code, Status: detail.Status}
	if detail.RetryAfterSeconds != nil && *detail.RetryAfterSeconds >= 0 {
		result.RetryAfterSeconds = detail.RetryAfterSeconds
	}
	return result
}
