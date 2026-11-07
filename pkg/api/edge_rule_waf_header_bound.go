package api

import "net/http"

// EdgeWAFCategoryUninspectedHeaders is the detection category a warn or
// block WAF rule reports for a request whose headers exceed what a check
// scores (ADR-831 amendment 20).
const EdgeWAFCategoryUninspectedHeaders = "uninspected-headers"

// EdgeWAFHeadersExceedInspection reports whether a WAF check would leave part
// of h unscored: a value longer than EdgeWAFMaxInspectHeaderValueBytes, or
// names and values past EdgeWAFMaxInspectHeaderBytes in all (ADR-831
// amendment 19). The bound keeps checks cheap, but content past it passed
// unchecked, so a client could pad its headers ("A1: 4 KiB" four times, or
// 4 KiB of filler before a payload) and hide an attack from a block rule.
// Warn and block rules count such a request as a detection (amendment 20).
func EdgeWAFHeadersExceedInspection(h http.Header) bool {
	total := 0
	for name, values := range h {
		for _, v := range values {
			if len(v) > EdgeWAFMaxInspectHeaderValueBytes {
				return true
			}
			total += len(name) + len(v)
		}
	}
	return total > EdgeWAFMaxInspectHeaderBytes
}
