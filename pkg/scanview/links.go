package scanview

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const brokenLink = "broken"

// Resolve component by component. Cleaning X/../Y before resolving X would
// change guest behavior when X is itself a symlink. Root-relative absolute links
// never reach the host, and '..' at the virtual root stays at that root.
func virtualLink(ctx context.Context, root *os.Root, name, target string) (string, error) {
	parts, queue, err := linkQueue(path.Dir(name), target)
	if err != nil {
		return "", err
	}
	r := &linkResolver{root: root, parts: parts, queue: queue}
	for len(r.queue) > 0 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		item := r.queue[0]
		r.queue = r.queue[1:]
		if item == "" || item == "." {
			continue
		}
		if item == ".." {
			if len(r.parts) > 0 {
				r.parts = r.parts[:len(r.parts)-1]
			}
			continue
		}
		broken, err := r.follow(item)
		if err != nil {
			return "", err
		}
		if broken {
			return brokenLink, nil
		}
	}
	return "/" + strings.Join(r.parts, "/"), nil
}

type linkResolver struct {
	root         *os.Root
	parts, queue []string
	hops         int
}

func (r *linkResolver) follow(item string) (bool, error) {
	candidate := path.Join(append(r.parts, item)...)
	if len(candidate) > api.ApplicationStandardScanMaxPathBytes {
		return false, ErrLimit
	}
	info, err := r.root.Lstat(candidate)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		r.hops++
		if r.hops >= api.ApplicationStandardRuntimeScanMaxSymlinkHops {
			return true, nil
		}
		next, err := r.root.Readlink(candidate)
		if err != nil {
			return false, err
		}
		prefix, pending, err := linkQueue(strings.Join(r.parts, "/"), next)
		if err != nil {
			return false, err
		}
		r.parts, r.queue = prefix, append(pending, r.queue...)
		return false, nil
	}
	if len(r.queue) > 0 && !info.IsDir() {
		return true, nil
	}
	r.parts = append(r.parts, item)
	return false, nil
}

func linkQueue(parent, target string) ([]string, []string, error) {
	if target == "" || len(target) > api.ApplicationStandardScanMaxPathBytes {
		return nil, nil, ErrInvalid
	}
	var prefix []string
	if !path.IsAbs(target) && parent != "" && parent != "." {
		prefix = strings.Split(parent, "/")
	}
	return prefix, strings.Split(target, "/"), nil
}
