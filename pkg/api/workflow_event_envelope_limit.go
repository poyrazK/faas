package api

import (
	"encoding/json"
	"strconv"
	"unicode/utf8"
)

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
	return WorkflowJSONBFits(payload, WorkflowRunInputMaxBytes)
}

// WorkflowJSONBFits validates jsonb compatibility and bounds its expanded size.
func WorkflowJSONBFits(payload []byte, maxBytes int64) bool {
	return workflowJSONBFits(payload, maxBytes)
}

// WorkflowJSONBCompatible reports whether a JSON document can be stored by
// PostgreSQL jsonb, without imposing the workflow input envelope size limit.
func WorkflowJSONBCompatible(payload []byte) bool {
	return workflowJSONBFits(payload, 0)
}

func workflowJSONBFits(payload []byte, maxBytes int64) bool {
	if !json.Valid(payload) || !utf8.Valid(payload) {
		return false
	}
	size := int64(len(payload))
	if maxBytes > 0 && size > maxBytes {
		return false
	}
	for i := 0; i < len(payload); {
		if payload[i] == '"' {
			i++
			for i < len(payload) {
				if payload[i] == '\\' {
					if payload[i+1] != 'u' {
						i += 2
						continue
					}
					value, _ := strconv.ParseUint(string(payload[i+2:i+6]), 16, 16)
					if value == 0 || value >= 0xdc00 && value <= 0xdfff {
						return false
					}
					i += 6
					if value >= 0xd800 && value <= 0xdbff {
						if i+6 > len(payload) || payload[i] != '\\' || payload[i+1] != 'u' {
							return false
						}
						low, err := strconv.ParseUint(string(payload[i+2:i+6]), 16, 16)
						if err != nil || low < 0xdc00 || low > 0xdfff {
							return false
						}
						i += 6
					}
				} else if payload[i] == '"' {
					i++
					break
				} else {
					i++
				}
			}
			continue
		}
		// jsonb output inserts a space after each object separator and comma.
		// A canonical jsonb value already has that space and must remain valid
		// when read and saved again at the same limit.
		if maxBytes > 0 && (payload[i] == ':' || payload[i] == ',') && (i+1 == len(payload) || payload[i+1] != ' ') {
			size++
			if size > maxBytes {
				return false
			}
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
		if extra := expanded - int64(i-start); maxBytes > 0 && extra > 0 {
			size += extra
			if size > maxBytes {
				return false
			}
		}
	}
	return true
}
