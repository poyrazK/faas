package region

import (
	"fmt"
	"strings"
	"time"
)

const RuntimeLogArchivePrefix = "faas-logs/"

// RuntimeLogArchiveKey keeps the established daily layout inside placement.
func RuntimeLogArchiveKey(id, instance, day string) string {
	if len(day) != 10 {
		return ArtifactKey(id, RuntimeLogArchivePrefix+instance+"/"+day+".jsonl.gz")
	}
	return ArtifactKey(id, RuntimeLogArchivePrefix+instance+"/"+day[:4]+"/"+day[5:7]+"/"+day+".jsonl.gz")
}

func ValidRuntimeLogInstance(instance string) bool {
	if instance == "" || len(instance) > 128 || instance == "." || instance == ".." {
		return false
	}
	for _, c := range instance {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._-", c) {
			continue
		}
		return false
	}
	return true
}

func ValidRuntimeLogDay(day string) bool {
	t, err := time.Parse("2006-01-02", day)
	return err == nil && t.Year() > 0 && t.Format("2006-01-02") == day
}

func ParseRuntimeLogArchiveKey(key string) (id, instance, day string, err error) {
	id, relative, err := SplitArtifactKey(key)
	if err != nil {
		return "", "", "", err
	}
	parts := strings.Split(relative, "/")
	if len(parts) != 5 || parts[0]+"/" != RuntimeLogArchivePrefix || !strings.HasSuffix(parts[4], ".jsonl.gz") {
		return "", "", "", fmt.Errorf("invalid runtime log archive key")
	}
	instance, day = parts[1], strings.TrimSuffix(parts[4], ".jsonl.gz")
	if !ValidRuntimeLogInstance(instance) || !ValidRuntimeLogDay(day) || key != RuntimeLogArchiveKey(id, instance, day) {
		return "", "", "", fmt.Errorf("invalid runtime log archive identity")
	}
	return id, instance, day, nil
}
