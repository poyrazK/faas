package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

type exportDiffValue struct {
	kind    byte
	scalar  string
	digest  [sha256.Size]byte
	members map[string]exportDiffValue
}

func writeExportDiffFrame(h hash.Hash, value string) {
	_, _ = io.WriteString(h, strconv.Itoa(len(value))+":")
	_, _ = io.WriteString(h, value)
}

func exportDiffScalar(kind byte, value string) exportDiffValue {
	h := sha256.New()
	writeExportDiffFrame(h, string(kind))
	writeExportDiffFrame(h, value)
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	return exportDiffValue{kind: kind, scalar: value, digest: digest}
}

// Normalize coefficient/exponent strings without float conversion or expanding
// exponent-sized integers. Equivalent decimal spellings share one identity.
func canonicalExportDiffNumber(value string) string {
	negative := ""
	if strings.HasPrefix(value, "-") {
		negative = "-"
		value = value[1:]
	}
	mantissa, power, found := strings.Cut(strings.ToLower(value), "e")
	var exponent big.Int
	if found {
		exponent.SetString(power, 10)
	}
	whole, fraction, _ := strings.Cut(mantissa, ".")
	digits := strings.TrimLeft(whole+fraction, "0")
	if digits == "" {
		return "0"
	}
	coefficient := strings.TrimRight(digits, "0")
	exponent.Add(&exponent, big.NewInt(int64(len(digits)-len(coefficient)-len(fraction))))
	return negative + coefficient + "e" + exponent.String()
}

func readExportDiffValue(decoder *json.Decoder) (exportDiffValue, error) {
	return readExportDiffValueMode(decoder, false)
}

func readExportDiffValueMode(decoder *json.Decoder, explain bool) (exportDiffValue, error) {
	token, err := decoder.Token()
	if err != nil {
		return exportDiffValue{}, errors.New("invalid JSON value")
	}
	switch value := token.(type) {
	case string:
		return exportDiffScalar('s', value), nil
	case json.Number:
		return exportDiffScalar('n', canonicalExportDiffNumber(string(value))), nil
	case bool:
		return exportDiffScalar('b', strconv.FormatBool(value)), nil
	case nil:
		return exportDiffScalar('z', ""), nil
	case json.Delim:
		if value == '{' {
			return readExportDiffObjectMode(decoder, explain)
		}
		if value == '[' {
			return readExportDiffArray(decoder)
		}
	}
	return exportDiffValue{}, errors.New("invalid JSON value")
}

func readExportDiffObjectMode(decoder *json.Decoder, explain bool) (exportDiffValue, error) {
	members := make(map[string]exportDiffValue)
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok {
			return exportDiffValue{}, errors.New("invalid JSON object")
		}
		if _, duplicate := members[name]; duplicate {
			return exportDiffValue{}, errors.New("duplicate JSON object field")
		}
		child, err := readExportDiffValueMode(decoder, explain)
		if err != nil {
			return exportDiffValue{}, err
		}
		if !explain {
			child.members = nil // The default index needs only the record digest.
		}
		members[name] = child
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return exportDiffValue{}, errors.New("invalid JSON object")
	}
	return exportDiffObjectValue(members), nil
}

func exportDiffObjectValue(members map[string]exportDiffValue) exportDiffValue {
	keys := make([]string, 0, len(members))
	for key := range members {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	h := sha256.New()
	writeExportDiffFrame(h, "object")
	for _, key := range keys {
		writeExportDiffFrame(h, key)
		child := members[key]
		_, _ = h.Write(child.digest[:])
	}
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	return exportDiffValue{kind: 'o', digest: digest, members: members}
}

func readExportDiffArray(decoder *json.Decoder) (exportDiffValue, error) {
	h := sha256.New()
	writeExportDiffFrame(h, "array")
	for decoder.More() {
		child, err := readExportDiffValue(decoder)
		if err != nil {
			return exportDiffValue{}, err
		}
		_, _ = h.Write(child.digest[:])
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
		return exportDiffValue{}, errors.New("invalid JSON array")
	}
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	return exportDiffValue{kind: 'a', digest: digest}, nil
}

// json.Valid has already checked escape syntax. Reject unpaired UTF-16 escapes
// instead of letting the decoder collapse different keys to replacement runes.
func exportDiffUnicodeValid(line []byte) bool {
	for i := 0; i < len(line); i++ {
		if line[i] != '\\' {
			continue
		}
		i++
		if line[i] != 'u' {
			continue
		}
		unit, err := strconv.ParseUint(string(line[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if unit >= 0xdc00 && unit <= 0xdfff {
			return false
		}
		if unit < 0xd800 || unit > 0xdbff {
			continue
		}
		if i+6 >= len(line) || line[i+1] != '\\' || line[i+2] != 'u' {
			return false
		}
		low, err := strconv.ParseUint(string(line[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}
