package api

import (
	"net/http"
	"strings"
)

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
	for name, values := range EdgeWAFInspectHeaders(h) {
		for _, v := range values {
			if len(v) > EdgeWAFMaxInspectHeaderValueBytes {
				return true
			}
			total += len(name) + len(v)
		}
	}
	return total > EdgeWAFMaxInspectHeaderBytes
}

// EdgeWAFInspectHeaders is h as a WAF check measures and scores it: every
// Cookie header is split into one value per cookie. A browser sends all of a
// site's cookies in one Cookie header, often more than the 4 KiB value bound,
// while it caps each cookie at 4 KiB, so bounding the joined header would cut
// cookies off unscored and make warn and block rules refuse ordinary
// visitors (ADR-831 amendment 20). The total bound still applies.
func EdgeWAFInspectHeaders(h http.Header) http.Header {
	cookies := h.Values("Cookie")
	if len(cookies) == 0 {
		return h
	}
	out := make(http.Header, len(h))
	for name, values := range h {
		if name != "Cookie" {
			out[name] = values
		}
	}
	for _, header := range cookies {
		for _, cookie := range strings.Split(header, ";") {
			if cookie = strings.TrimSpace(cookie); cookie != "" {
				out["Cookie"] = append(out["Cookie"], cookie)
			}
		}
	}
	return out
}
