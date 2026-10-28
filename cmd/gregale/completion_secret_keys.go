package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Parse only supported completion positions, never execute the supplied words.
func secretKeyCompletionTarget(words []string) (app, scope, prefix, inline string, ok bool) {
	if len(words) > 0 && words[0] == "--" {
		words = words[1:]
	}
	if len(words) < 3 || words[0] != "secrets" {
		return
	}
	verb := words[1]
	if verb != "list" && verb != "unset" && verb != "rm" {
		return
	}
	current := words[len(words)-1]
	previous := words[2 : len(words)-1]
	positionals := 0
	expecting := ""
	separated := false
	for _, word := range previous {
		if expecting != "" {
			if strings.HasPrefix(word, "-") {
				return
			}
			switch expecting {
			case "app":
				app = word
			case "scope", "env":
				scope = word
			}
			expecting = ""
			continue
		}
		if word == "--" {
			separated = true
			continue
		}
		if !separated && strings.HasPrefix(word, "-") {
			name, value, hasValue := strings.Cut(strings.TrimLeft(word, "-"), "=")
			switch name {
			case "app", "scope", "env", "key", "class", "older-than", "delivery-status", "interval", "timeout":
				if hasValue {
					if name == "app" {
						app = value
					}
					if name == "scope" || name == "env" { // --env is scope's canonical spelling
						scope = value
					}
				} else {
					expecting = name
				}
			case "watch", "restart", "wait-for-ack", "interactive", "dry-run":
				if name == "interactive" && (!hasValue || value != "false") {
					return
				}
			default:
				return
			}
			continue
		}
		positionals++
	}
	if verb == "list" {
		if positionals != 0 || separated || (expecting != "" && expecting != "key") {
			return
		}
		if expecting == "key" {
			prefix = current
		} else if strings.HasPrefix(current, "--key=") {
			prefix = strings.TrimPrefix(current, "--key=")
			inline = "--key="
		} else if strings.HasPrefix(current, "-key=") {
			prefix = strings.TrimPrefix(current, "-key=")
			inline = "-key="
		} else {
			return
		}
	} else {
		if expecting != "" || (!separated && strings.HasPrefix(current, "-")) {
			return
		}
		prefix = current
	}
	ok = true
	return
}

type secretKeyCompletionCache struct {
	At   time.Time `json:"at"`
	Keys []string  `json:"keys"`
}

func cmdCompletionSecretKeys(words []string, contextOnly bool) int {
	if len(words) > 0 && words[0] == "--" {
		words = words[1:]
	}
	app, scope, prefix, inline, ok := secretKeyCompletionTarget(words)
	if !ok {
		return 3
	}
	if contextOnly {
		return 0
	}
	app, err := resolveSecretApp(app)
	if err != nil || !api.ValidAppSlug(app) {
		return 0
	}
	scope, err = resolveEnvironmentFlagOrContext(scope)
	if err != nil {
		return 0
	}
	scope = scopeOrDefault(scope)
	if scope == "__all__" && words[1] != "list" {
		return 0
	}
	if scope != "__all__" && api.ValidateScope(scope) != nil {
		return 0
	}
	token := loadToken()
	if token == "" {
		return 0
	}
	// API credentials are high-entropy MAC keys, never part of cache metadata.
	// Domain separation keeps this fingerprint specific to completion caching.
	identity, _ := json.Marshal([]string{"gregale/secret-key-completion-cache/v2", apiBase(), currentProfile(), app, scope})
	fingerprint := hmac.New(sha256.New, []byte(token))
	_, _ = fingerprint.Write(identity)
	digest := fingerprint.Sum(nil)
	cachePath := filepath.Join(filepath.Dir(cachePathForScripts()), fmt.Sprintf("completion-cache-secret-keys-%x.json", digest))
	cache := secretKeyCompletionCache{}
	now := time.Now()
	data, err := os.ReadFile(cachePath)
	fresh := err == nil && json.Unmarshal(data, &cache) == nil && !cache.At.After(now) && now.Sub(cache.At) < 30*time.Second
	if !fresh {
		ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
		defer cancel()
		list, err := NewClient(apiBase(), token).ListSecretsWithScope(ctx, app, scope)
		if err != nil {
			return 0
		}
		keys := []string{}
		if len(list.SecretsByScope) > 0 {
			if scope != "__all__" {
				return 0
			}
			for _, rows := range list.SecretsByScope {
				for _, row := range rows {
					if api.ValidateSecretKey(row.Key) == nil {
						keys = append(keys, row.Key)
					}
				}
			}
		} else {
			for _, row := range list.Secrets {
				if (scope == "__all__" || scopeOrDefault(row.Scope) == scope) && api.ValidateSecretKey(row.Key) == nil {
					keys = append(keys, row.Key)
				}
			}
		}
		cache = secretKeyCompletionCache{At: now, Keys: sortedUniqueStrings(keys)}
		if os.MkdirAll(filepath.Dir(cachePath), 0700) == nil {
			if f, err := os.CreateTemp(filepath.Dir(cachePath), ".secret-keys-*"); err == nil {
				path := f.Name()
				_ = f.Chmod(0600)
				encodeErr := json.NewEncoder(f).Encode(cache)
				closeErr := f.Close()
				if encodeErr == nil && closeErr == nil {
					_ = os.Rename(path, cachePath)
				}
				_ = os.Remove(path)
			}
		}
	}
	for _, key := range cache.Keys {
		if api.ValidateSecretKey(key) == nil && strings.HasPrefix(key, prefix) {
			_, _ = fmt.Fprintln(osStdout, inline+key)
		}
	}
	return 0
}
