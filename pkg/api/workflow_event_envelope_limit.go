package api

import "strconv"

// PostgreSQL jsonb stores JSON numbers as numeric, whose maximum precision and
// scale are smaller than the workflow's overall input-size limit.
const (
	workflowJSONNumericIntegerDigits = 131072
	workflowJSONNumericScale         = 16383
)

// WorkflowEventEnvelopeFits bounds a JSON envelope as well as the possible
// expansion of exponent-form numbers when PostgreSQL stores it as jsonb.
// Callers provide valid JSON. The bound is deliberately conservative for
// numbers whose leading or trailing zeroes PostgreSQL might discard.
func WorkflowEventEnvelopeFits(payload []byte) bool {
	size := int64(len(payload))
	if size > WorkflowRunInputMaxBytes {
		return false
	}
	for i := 0; i < len(payload); {
		if payload[i] == '"' {
			i++
			for i < len(payload) {
				if payload[i] == '\\' {
					i += 2
				} else if payload[i] == '"' {
					i++
					break
				} else {
					i++
				}
			}
			continue
		}
		if payload[i] != '-' && (payload[i] < '0' || payload[i] > '9') {
			i++
			continue
		}
		start := i
		negative := payload[i] == '-'
		if negative {
			i++
		}
		var digits, fraction int64
		for i < len(payload) && payload[i] >= '0' && payload[i] <= '9' {
			digits++
			i++
		}
		if i < len(payload) && payload[i] == '.' {
			i++
			for i < len(payload) && payload[i] >= '0' && payload[i] <= '9' {
				digits++
				fraction++
				i++
			}
		}
		var exponent int64
		if i < len(payload) && (payload[i] == 'e' || payload[i] == 'E') {
			i++
			exponentStart := i
			if i < len(payload) && (payload[i] == '+' || payload[i] == '-') {
				i++
			}
			for i < len(payload) && payload[i] >= '0' && payload[i] <= '9' {
				i++
			}
			var err error
			exponent, err = strconv.ParseInt(string(payload[exponentStart:i]), 10, 64)
			if err != nil || exponent > WorkflowRunInputMaxBytes || exponent < -WorkflowRunInputMaxBytes {
				return false
			}
		}
		scale := fraction - exponent
		if scale > workflowJSONNumericScale || digits-scale > workflowJSONNumericIntegerDigits {
			return false
		}
		expanded := digits
		if scale <= 0 {
			expanded -= scale
		} else if scale < digits {
			expanded++ // decimal point
		} else {
			expanded = scale + 2 // leading zero and decimal point
		}
		if negative {
			expanded++
		}
		if extra := expanded - int64(i-start); extra > 0 {
			size += extra
			if size > WorkflowRunInputMaxBytes {
				return false
			}
		}
	}
	return true
}
