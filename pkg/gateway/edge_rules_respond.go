package gateway

// Edge rule kind=respond subset. A compiled respond rule is a bounded JSON
// response selected by the normal host/path/method matcher. The handler
// applies it only after the app authentication gates and only for preview
// applications.

// EdgeRuleRespondResolved is the kind=respond subset used by the gateway.
// Body is copied while compiling so the host cache never aliases the state
// store's JSON buffer.
type EdgeRuleRespondResolved struct {
	ID         string
	AccountID  string
	AppID      string
	Priority   int
	PathGlob   string
	Methods    map[string]bool
	StatusCode int
	Body       []byte
}

// PickFirstRespondMatch returns the priority-ordered respond rule matching
// the request path and method.
func PickFirstRespondMatch(rules []EdgeRuleRespondResolved, requestPath, method string) *EdgeRuleRespondResolved {
	for i := range rules {
		r := &rules[i]
		if r.Methods != nil && !r.Methods[method] {
			continue
		}
		if r.PathGlob != "" {
			ok, _ := pathGlobMatch(r.PathGlob, requestPath)
			if !ok {
				continue
			}
		}
		return r
	}
	return nil
}
