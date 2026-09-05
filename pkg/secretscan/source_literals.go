package secretscan

import (
	"bytes"
	"path/filepath"
	"strings"
)

// Source expressions and prose can have high character entropy without
// containing credentials. Provider signatures still scan the complete value;
// generic entropy applies to individual tokens and quoted literals. The .env
// parser retains its existing whole-value behavior, including quoted spaces.
func matchSourceValue(file string, line int, key string, value []byte, inPEMBlock bool) *Finding {
	if strings.HasPrefix(filepath.Base(file), ".env") {
		return matchValue(file, line, key, value, inPEMBlock)
	}
	// Suppress only entropy here; explicit provider and PEM patterns still run.
	if f := matchValue(file, line, key, value, true); f != nil {
		return f
	}
	if inPEMBlock {
		return nil
	}
	value = bytes.TrimSpace(value)
	if sourceEntropyToken(value) {
		return matchValue(file, line, key, value, false)
	}
	for pos := 0; pos < len(value); pos++ {
		quote := value[pos]
		if quote != '"' && quote != '\'' && quote != '`' {
			continue
		}
		start := pos + 1
		for pos++; pos < len(value); pos++ {
			if value[pos] == '\\' {
				pos++
				continue
			}
			if value[pos] != quote {
				continue
			}
			literal := value[start:pos]
			if sourceEntropyToken(literal) {
				if f := matchValue(file, line, key, literal, false); f != nil {
					return f
				}
			}
			break
		}
	}
	return nil
}

func sourceEntropyToken(value []byte) bool {
	if len(value) < entropyMinLen {
		return false
	}
	for _, c := range value {
		if c <= ' ' || c >= 127 {
			return false
		}
		switch c {
		case '{', '}', '[', ']', '(', ')', ';', ',', ':', '"', '\'', '`', '\\':
			return false
		}
	}
	return true
}
