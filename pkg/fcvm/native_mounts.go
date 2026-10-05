package fcvm

import (
	"errors"
	"path/filepath"
	"strings"
)

func nativeMountsBelow(data []byte, root string) ([]string, error) {
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) || root == "/" {
		return nil, errors.New("native recovery: invalid mount proof root")
	}
	var mounts []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			return nil, errors.New("native recovery: incomplete mount table")
		}
		separator := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				separator = i
				break
			}
		}
		if separator < 6 || len(fields)-separator < 4 {
			return nil, errors.New("native recovery: incomplete mount table entry")
		}
		path, err := decodeNativeMountPath(fields[4])
		if err != nil {
			return nil, err
		}
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return nil, errors.New("native recovery: invalid mount table path")
		}
		if path == root || strings.HasPrefix(path, root+string(filepath.Separator)) {
			mounts = append(mounts, path)
		}
	}
	return mounts, nil
}

func decodeNativeMountPath(value string) (string, error) {
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			out.WriteByte(value[i])
			continue
		}
		if i+3 >= len(value) {
			return "", errors.New("native recovery: invalid mount escape")
		}
		switch value[i+1 : i+4] {
		case "040":
			out.WriteByte(' ')
		case "011":
			out.WriteByte('\t')
		case "012":
			out.WriteByte('\n')
		case "134":
			out.WriteByte('\\')
		default:
			return "", errors.New("native recovery: unknown mount escape")
		}
		i += 3
	}
	return out.String(), nil
}
