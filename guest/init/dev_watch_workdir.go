package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// devWatchAppRoot is where a Railpack watch-mode image keeps the source. Only
// directories at or below it are handed to the app user.
const devWatchAppRoot = "/app"

// devWatchOwnedDirs are the directories, relative to the working directory, a
// development server creates its build output and caches in: Next.js makes
// .next and Nuxt .nuxt in the working directory, Vite keeps
// node_modules/.vite. The image owns them as root, so the app user could not
// create a single entry in them (next dev: EACCES mkdir /app/.next, prod
// 2026-11-21).
var devWatchOwnedDirs = []string{".", "node_modules"}

// prepareDevWatchWorkdir lets the app user create entries in its working
// directory and node_modules in a watch-mode developer image. It changes the
// owner of those directories only, never their contents, so on the image's
// overlay only directory metadata is copied up. A symbolic link is never
// followed, and a working directory outside /app is left alone.
func prepareDevWatchWorkdir(dir string, uid, gid uint32) error {
	return prepareDevWatchWorkdirIn(devWatchAppRoot, dir, uid, gid, os.Lchown)
}

func prepareDevWatchWorkdirIn(appRoot, dir string, uid, gid uint32, lchown func(string, int, int) error) error {
	dir = filepath.Clean(dir)
	if dir != appRoot && !strings.HasPrefix(dir, appRoot+string(filepath.Separator)) {
		return nil
	}
	for _, rel := range devWatchOwnedDirs {
		path := filepath.Join(dir, rel)
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect %s: %w", path, err)
		}
		if !info.IsDir() {
			continue
		}
		if err := lchown(path, int(uid), int(gid)); err != nil {
			return fmt.Errorf("hand %s to the app user: %w", path, err)
		}
	}
	return nil
}
