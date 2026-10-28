package journeys

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestAccountAndKeys covers the signed-in account surface: identity, plan
// limits, scoped API keys end to end, usage, invoices and the audit log
// (inventory rows 138, 140, 142, 144, 151).
func TestAccountAndKeys(t *testing.T) {
	tg := needs(t, tierSmoke, true)
	t.Parallel()
	s := newStory(t)

	s.step("whoami", func(t *testing.T) {
		who := object(t, tg.ok(t, time.Minute, "whoami").stdout)
		if str(who, "email") == "" || str(who, "plan") == "" || str(who, "status") != "active" {
			t.Fatalf("whoami: %s", compact(map[string]any{"plan": field(who, "plan"), "status": field(who, "status")}))
		}
		if num(who, "limits.deployed_apps") <= 0 {
			t.Fatalf("whoami has no limits.deployed_apps")
		}
	})
	s.check("capabilities", func(t *testing.T) {
		if docs := objects(t, tg.ok(t, time.Minute, "capabilities").stdout); len(docs) == 0 {
			t.Fatal("gregale capabilities printed nothing")
		}
	})
	s.check("usage_and_invoices", func(t *testing.T) {
		objects(t, tg.ok(t, time.Minute, "usage").stdout)
		objects(t, tg.ok(t, time.Minute, "invoices").stdout)
	})

	var keyID, key string
	label := tg.slug("key")
	s.step("mint_scoped_key", func(t *testing.T) {
		res := tg.ok(t, time.Minute, "keys", "add", label, "--scopes", "apps:read")
		doc := object(t, res.stdout)
		keyID = str(doc, "id")
		key = apiKeyRe.FindString(string(res.stdout))
		if keyID == "" || key == "" {
			t.Fatalf("keys add printed no id or key: %s", tg.redact(compact(doc)))
		}
	})
	// Registered on the journey, not the step: a step's cleanup runs when
	// the step ends, which would revoke the key before it is used.
	t.Cleanup(func() {
		if keyID != "" {
			tg.run(t, time.Minute, "keys", "rm", keyID)
		}
	})
	s.step("scoped_key_reads", func(t *testing.T) {
		if res := tg.runAs(t, key, time.Minute, "apps"); res.exit != 0 {
			t.Fatalf("apps:read key cannot list apps: %s", res.problem())
		}
	})
	s.check("scoped_key_cannot_admin", func(t *testing.T) {
		res := tg.runAs(t, key, time.Minute, "keys", "add", tg.slug("escalate"), "--scopes", "admin")
		if res.exit == 0 {
			if leaked := apiKeyRe.FindString(string(res.stdout)); leaked != "" {
				id := str(object(t, res.stdout), "id")
				tg.run(t, time.Minute, "keys", "rm", id)
			}
			t.Fatal("an apps:read key minted an admin key")
		}
		if p := res.problem(); !strings.Contains(p, "forbidden") && !strings.Contains(p, "scope") {
			t.Fatalf("want a scope/forbidden error, got: %s", p)
		}
	})
	s.check("audit_log_records_key", func(t *testing.T) {
		eventually(t, "key creation in the audit log", 2*time.Minute, 5*time.Second, func() (bool, string) {
			res := tg.run(t, time.Minute, "audit-events", "list", "--limit", "100")
			if res.exit != 0 {
				return false, res.problem()
			}
			return strings.Contains(string(res.stdout), keyID), "no audit event mentions key " + keyID
		})
	})
	s.step("revoke_key", func(t *testing.T) {
		tg.ok(t, time.Minute, "keys", "rm", keyID)
		revoked := keyID
		keyID = ""
		eventually(t, "revoked key rejected", time.Minute, 3*time.Second, func() (bool, string) {
			res := tg.runAs(t, key, time.Minute, "apps")
			p := res.problem()
			return res.exit != 0 && (strings.Contains(p, "api_key_revoked") || strings.Contains(p, "unauthorized")),
				"key " + revoked + " not rejected as revoked: exit " + strconv.Itoa(res.exit) + " " + p
		})
	})
}
