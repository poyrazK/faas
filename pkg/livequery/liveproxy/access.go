package liveproxy

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/livequery/liveproto"
	"github.com/onebox-faas/faas/pkg/livequery/rest"
)

// Live reads without a user token (ADR-20261016151947) follow the Data API's
// own rules, which its runtime reports and apid puts on the route: anonymous
// reads only of public relations (embedded ones included), server keys only
// within their permissions. Admission replaces the credential with claims
// the hub trusts from realtimed alone; PostgreSQL row-level security still
// decides every row. The server key itself never leaves realtimed.

// keyless reports whether a frame reads without a user token.
func keyless(f liveproto.ClientFrame) bool { return f.Anonymous || f.ServerKey != "" }

// keylessKind is the access kind a keyless frame asks for.
func keylessKind(f liveproto.ClientFrame) string {
	if f.Anonymous {
		return liveproto.AccessAnonymous
	}
	return liveproto.AccessServer
}

// admitKeyless checks an anonymous or server-key read of read (in schema,
// "" for the Data API's default) and turns f into the hub's frame. A
// renewal passes its subscription's read, so it is checked again.
func admitKeyless(route Route, f *liveproto.ClientFrame, read, schema string, now time.Time) (string, string) {
	access := route.Access
	if access == nil || len(access.Schemas) == 0 {
		return liveproto.CodeForbidden, "the app's Data API has not reported its access settings; turn usage reporting on and restart it"
	}
	if schema == "" {
		schema = access.Schemas[0]
	}
	if !slices.Contains(access.Schemas, schema) {
		return liveproto.CodeForbidden, "the Data API does not expose that schema"
	}
	relations, err := readRelations(read)
	if err != nil {
		return liveproto.CodeInvalidQuery, err.Error()
	}
	claims := map[string]string{}
	if f.Anonymous {
		for _, relation := range relations {
			if !slices.Contains(access.PublicRead, schema+"."+relation) {
				return liveproto.CodeForbidden, "the read includes a relation that is not publicly readable: " + schema + "." + relation
			}
		}
		claims["gregale_access"] = liveproto.AccessAnonymous
	} else {
		key, ok := matchServerKey(access.ServerKeys, f.ServerKey)
		if !ok {
			return liveproto.CodeSealRejected, "the server key was not accepted"
		}
		if p := key.Permissions; p != nil {
			if f.Subject != "" && p.Subjects != nil && !*p.Subjects {
				return liveproto.CodeForbidden, "this server key may not act for a subject"
			}
			for _, relation := range relations {
				if p.Relations != nil && !slices.Contains(p.Relations, schema+"."+relation) {
					return liveproto.CodeForbidden, "the read is outside the server key's permissions: " + schema + "." + relation
				}
			}
		}
		claims["gregale_access"], claims["gregale_server_key"] = liveproto.AccessServer, key.Slot
		if f.Subject != "" {
			claims["sub"] = f.Subject
		}
	}
	encoded, err := json.Marshal(claims)
	if err != nil {
		return liveproto.CodeInternal, "encode the claims"
	}
	f.Access = keylessKind(*f)
	f.Claims, f.ClaimsExpires = encoded, now.Add(api.LiveQueryKeySessionMax).Unix()
	f.Anonymous, f.ServerKey, f.Subject = false, "", ""
	if f.Type == liveproto.TypeSubscribe {
		f.Schema = schema // compile exactly the schema that was checked
	}
	return "", ""
}

// dataAPIDefaultSchema is the schema the hub reads when a frame names none
// (liveserver's defaultDataAPISchema), the Data API's own default.
const dataAPIDefaultSchema = "api"

// admitTokenSchema holds a read with a user token to the schemas the app's
// Data API exposes, as admitKeyless does. The binding login may hold grants
// on a schema the Data API keeps unexposed, such as base tables behind
// security-invoker views; a live read naming that schema would read past the
// boundary REST keeps. Until the Data API reports its settings, only its
// default schema is read.
func admitTokenSchema(route Route, f *liveproto.ClientFrame) (string, string) {
	access := route.Access
	if access == nil || len(access.Schemas) == 0 {
		if f.Schema != "" && f.Schema != dataAPIDefaultSchema {
			return liveproto.CodeForbidden, "the app's Data API has not reported which schemas it exposes; live reads use its default schema until it does"
		}
		return "", ""
	}
	if f.Schema == "" {
		f.Schema = access.Schemas[0]
	}
	if !slices.Contains(access.Schemas, f.Schema) {
		return liveproto.CodeForbidden, "the Data API does not expose that schema"
	}
	return "", ""
}

// readRelations lists the relation a Data API read names and every relation
// it embeds.
func readRelations(read string) ([]string, error) {
	req, err := rest.Parse(read)
	if err != nil {
		return nil, err
	}
	relations := []string{req.Relation}
	var walk func(items []rest.Item)
	walk = func(items []rest.Item) {
		for _, item := range items {
			if item.Embed != nil {
				relations = append(relations, item.Embed.Relation)
				walk(item.Embed.Select)
			}
		}
	}
	walk(req.Select)
	return relations, nil
}

// matchServerKey finds the slot whose hash matches key, comparing every slot.
func matchServerKey(keys []api.DataAPIServerKey, key string) (api.DataAPIServerKey, bool) {
	sum := sha256.Sum256([]byte(key))
	var match api.DataAPIServerKey
	found := false
	for _, k := range keys {
		want, err := hex.DecodeString(k.SHA256)
		if err == nil && len(want) == len(sum) && subtle.ConstantTimeCompare(sum[:], want) == 1 && !found {
			match, found = k, true
		}
	}
	return match, found
}
