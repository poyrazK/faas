package s3gateway

import (
	"testing"
)

func TestDeleteObjectsStrictSelectors(t *testing.T) {
	for _, body := range []string{
		`<Delete><Object><Key>key</Key><Key>other</Key></Object></Delete>`,
		`<Delete><Object><Key>key</Key><VersionId>null</VersionId><VersionId>null</VersionId></Object></Delete>`,
		`<Delete><Object><Key>key</Key><ETag>condition</ETag></Object></Delete>`,
		`<Delete><Object><Key>key</Key><LastModifiedTime>date</LastModifiedTime></Object></Delete>`,
		`<Delete><Object><Key><Nested>key</Nested></Key></Object></Delete>`,
		`<Delete><Object><Key>key</Key><VersionId/></Object></Delete>`,
		`<Delete><Object><Key>key</Key></Object><Quiet>maybe</Quiet></Delete>`,
		`<Delete><Object><Key>key</Key></Object><Unknown/></Delete>`,
		`<Delete><Object><Key>key</Key></Object></Delete><Delete/>`,
		`<Delete xmlns="https://wrong.test"><Object><Key>key</Key></Object></Delete>`,
		`<Delete ETag="predicate"><Object><Key>key</Key></Object></Delete>`,
	} {
		if _, err := parseDeleteObjects([]byte(body)); err == nil {
			t.Fatal("unsafe XML accepted", body)
		}
	}
	in, err := parseDeleteObjects([]byte(`<Delete xmlns="` + s3XMLNamespace + `"><Object><Key> key /+%.txt </Key><VersionId>null</VersionId></Object><Quiet>true</Quiet></Delete>`))
	if err != nil || !in.Quiet || len(in.Objects) != 1 || in.Objects[0].Key != " key /+%.txt " || in.Objects[0].VersionID != "null" {
		t.Fatal(in, err)
	}
}
