package objectinventory

import (
	"net"
	"strings"
)

func ValidBucket(name string) bool {
	if len(name) < 3 || len(name) > 63 || net.ParseIP(name) != nil || strings.Contains(name, "..") {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return false
			}
		}
	}
	return true
}
