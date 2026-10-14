package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func completeLogViewTargetApps(words []string, contextOnly bool) (int, bool) {
	if len(words) > 0 && words[0] == "--" {
		words = words[1:]
	}
	if len(words) < 4 || words[0] != "logs" {
		return 1, false
	}
	defaultsShow := words[1] == "defaults" && words[2] == "show"
	if !defaultsShow && (words[1] != "views" || (words[2] != "list" && words[2] != "show")) {
		return 1, false
	}
	prior, prefix, inline := words[3:len(words)-1], words[len(words)-1], ""
	if strings.HasPrefix(prefix, "--app=") {
		prefix, inline = strings.TrimPrefix(prefix, "--app="), "--app="
	} else if len(prior) > 0 && prior[len(prior)-1] == "--app" {
		prior = prior[:len(prior)-1]
	} else {
		return 1, false
	}
	nameSet := false
	for i := 0; i < len(prior); i++ {
		if !strings.HasPrefix(prior[i], "-") {
			if defaultsShow || words[2] != "show" || nameSet {
				return 1, false
			}
			nameSet = true
			continue
		}
		option, value, equals := strings.Cut(prior[i], "=")
		switch option {
		case "--details":
			if defaultsShow {
				return 1, false
			}
			if equals {
				if _, err := strconv.ParseBool(value); err != nil {
					return 1, false
				}
			}
		case "--app", "--source", "--name":
			if option != "--app" && words[2] != "list" {
				return 1, false
			}
			if !equals {
				i++
				if i >= len(prior) {
					return 1, false
				}
			}
		default:
			return 1, false
		}
	}
	if contextOnly {
		return 0, true
	}
	path := os.Getenv("FAAS_COMPLETION_CACHE_PATH")
	if path == "" {
		path = cachePathForScripts()
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() > 1<<20 {
		return 0, true
	}
	file, err := openCustomerFile(path)
	if err != nil {
		return 0, true
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || info.Size() > 1<<20 {
		return 0, true
	}
	var entry api.CompletionCacheEntry
	decoder := json.NewDecoder(io.LimitReader(file, (1<<20)+1))
	if decoder.Decode(&entry) != nil || entry.Version != 1 {
		return 0, true
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return 0, true
	}
	names := []string{}
	seen := map[string]bool{}
	for _, app := range entry.Apps {
		if api.ValidAppSlug(app.Slug) && !seen[app.Slug] && strings.HasPrefix(app.Slug, prefix) {
			names = append(names, app.Slug)
			seen[app.Slug] = true
		}
	}
	sort.Strings(names)
	for _, name := range names {
		_, _ = fmt.Fprintln(osStdout, inline+name)
	}
	return 0, true
}
