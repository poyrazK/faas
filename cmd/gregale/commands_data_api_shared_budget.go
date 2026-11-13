package main

import (
	"encoding/json"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func parseDataAPISharedBudget(value string) (string, error) {
	p := struct {
		Version  int    `json:"version"`
		Requests int    `json:"requests"`
		Window   int    `json:"window_seconds"`
		Claim    string `json:"claim,omitempty"`
	}{Version: 1}
	seen := map[string]bool{}
	invalid := errors.New("use requests=N,window=DURATION[,claim=JWT_CLAIM], or off")
	for _, part := range strings.Split(value, ",") {
		k, v, ok := strings.Cut(part, "=")
		if !ok || seen[k] {
			return "", invalid
		}
		seen[k] = true
		switch k {
		case "requests":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > api.DataAPISharedBudgetMaxRequests {
				return "", invalid
			}
			p.Requests = n
		case "window":
			d, err := time.ParseDuration(v)
			if err != nil || d < time.Second || d > api.DataAPISharedBudgetMaxWindowSeconds*time.Second || d%time.Second != 0 {
				return "", invalid
			}
			p.Window = int(d / time.Second)
		case "claim":
			if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`).MatchString(v) {
				return "", invalid
			}
			p.Claim = v
		default:
			return "", invalid
		}
	}
	if p.Requests == 0 || p.Window == 0 {
		return "", invalid
	}
	b, err := json.Marshal(p)
	return string(b), err
}
