package api

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestParseCacheTags(t *testing.T) {
	unique := make([]string, CacheTagMaxCount+1)
	for i := range unique {
		unique[i] = fmt.Sprintf("tag-%d", i)
	}
	tags, err := ParseCacheTags([]string{"Product:42, collection-winter", "product:42"})
	if err != nil || !reflect.DeepEqual(tags, []string{"collection-winter", "product:42"}) {
		t.Fatalf("ParseCacheTags = %v, %v", tags, err)
	}
	for _, values := range [][]string{
		{""}, {"product:42,"}, {"white space"}, {"éclair"},
		{strings.Repeat("x", CacheTagMaxBytes+1)},
		{strings.Repeat("x", CacheTagHeaderMaxBytes+1)},
		{strings.Join(unique, ",")},
	} {
		if _, err := ParseCacheTags(values); err == nil {
			t.Errorf("ParseCacheTags(%q) accepted invalid tags", values)
		}
	}
}
