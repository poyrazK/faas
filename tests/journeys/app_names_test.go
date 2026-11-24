package journeys

import (
	"strings"
	"testing"
	"time"
)

// App name journeys (docs/apps.md, "When the name you ask for is taken"): a deploy whose name another
// account holds, or that the operator's app_name_blocklist refuses, takes a
// suffixed name of the form <name>-<5 characters> that is the same on every
// deploy from the same owner; rename refuses both names.

// textMode switches one CLI run out of the harness's FAAS_JSON=1, for the
// notices a person reads on stderr.
var textMode = []string{"FAAS_JSON=0"}

// createOnly reserves an app named name without uploading a deployment and
// returns the slug it got.
func (tg *target) createOnly(t *testing.T, token, name string) string {
	t.Helper()
	res := tg.exec(t, token, t.TempDir(), 2*time.Minute, "deploy", "--create-only", "--app", "--name", name)
	if res.exit != 0 {
		t.Fatalf("deploy --create-only --name %s failed (exit %d): %s", name, res.exit, tg.redact(res.problem()))
	}
	return str(object(t, res.stdout), "slug")
}

// suffixedSlug asserts got is name with a five-character suffix. Like
// api.SuffixedAppSlugs, a name too long for the suffix is shortened to 34
// characters first.
func suffixedSlug(t *testing.T, name, got string) {
	t.Helper()
	stem := name
	if len(stem) > 34 {
		stem = strings.TrimRight(stem[:34], "-")
	}
	suffix, ok := strings.CutPrefix(got, stem+"-")
	if !ok || len(suffix) != 5 {
		t.Fatalf("deploy --name %s created %q, want %s-<5 characters>", name, got, stem)
	}
}

// removeApp deletes slug from the account token belongs to and purges it, so
// the next run can take the same name (a deleted app holds its slug for its
// restore window). GREGALE_E2E_KEEP=1 keeps a failed journey's app.
func (tg *target) removeApp(t *testing.T, token, slug string) {
	t.Helper()
	if slug == "" {
		return
	}
	if tg.keep && t.Failed() {
		t.Logf("keeping %s for debugging (GREGALE_E2E_KEEP=1)", slug)
		return
	}
	res := tg.runAs(t, token, 2*time.Minute, "app", "delete", slug, "--yes")
	if res.exit != 0 && !strings.Contains(res.problem(), "not_found") {
		t.Logf("warning: cleanup could not delete %s (%s); the next run's sweeper deletes it", slug, tg.redact(res.problem()))
		return
	}
	if res := tg.runAs(t, token, 2*time.Minute, "app", "purge", slug, "--yes"); res.exit != 0 {
		t.Logf("warning: cleanup could not purge %s (%s); its name stays held until the restore window ends", slug, tg.redact(res.problem()))
	}
}

// TestAppNameTakenByAnotherAccount: a second account holds a name; this
// account's deploy under that name gets a suffixed app instead of failing,
// gets the same one again on the next deploy, and is told why. Renaming onto
// the other account's name is refused.
func TestAppNameTakenByAnotherAccount(t *testing.T) {
	tg := needs(t, tierFull, true)
	if tg.otherToken == "" {
		t.Skip("needs a second account: set GREGALE_E2E_OTHER_TOKEN (see tests/journeys/README.md)")
	}
	t.Parallel()
	taken := tg.slug("taken")
	t.Cleanup(func() { tg.removeApp(t, tg.otherToken, taken) })
	s := newStory(t)
	if !s.step("other_account_takes_name", func(t *testing.T) {
		if got := tg.createOnly(t, tg.otherToken, taken); got != taken {
			t.Fatalf("the other account asked for %s and got %s", taken, got)
		}
	}) {
		return
	}
	tg.acquireAppSlot(t)
	var mine string
	t.Cleanup(func() {
		defer func() { <-tg.appSlots }()
		tg.removeApp(t, tg.token, mine)
	})
	if !s.step("deploy_gets_suffixed_name", func(t *testing.T) {
		mine = tg.createOnly(t, tg.token, taken)
		suffixedSlug(t, taken, mine)
	}) {
		return
	}
	s.check("same_suffix_again_with_notice", func(t *testing.T) {
		res := tg.execInEnv(t, tg.token, t.TempDir(), "", textMode, 2*time.Minute, "deploy", "--create-only", "--app", "--name", taken)
		if res.exit != 0 {
			t.Fatalf("second deploy --name %s failed (exit %d): %s", taken, res.exit, tg.redact(tail(string(res.stderr), 500)))
		}
		stderr := string(res.stderr)
		if !strings.Contains(stderr, "belongs to another account") || !strings.Contains(stderr, mine) {
			t.Fatalf("second deploy did not say %s belongs to another account and the app is %s: %s", taken, mine, tail(stderr, 500))
		}
	})
	s.check("other_account_keeps_its_app", func(t *testing.T) {
		shown := object(t, tg.runAs(t, tg.otherToken, time.Minute, "app", taken).stdout)
		if str(shown, "slug") != taken {
			t.Fatalf("the other account's app reads back as %s", compact(shown))
		}
	})
	s.check("rename_onto_taken_name_refused", func(t *testing.T) {
		res := tg.run(t, time.Minute, "app", mine, "rename", taken)
		if res.exit == 0 || res.problemCode() != "app_rename_failed" {
			t.Fatalf("rename %s -> %s: exit %d, problem %s; want app_rename_failed", mine, taken, res.exit, res.problem())
		}
	})
}

// TestAppNameBlocked: a name on the target's app_name_blocklist is not
// offered. A deploy under it gets a suffixed app and says the name is not
// available; rename onto it is refused with app_name_unavailable.
func TestAppNameBlocked(t *testing.T) {
	tg := needs(t, tierFull, true)
	if tg.blockedName == "" {
		t.Skip("needs a blocked name: set GREGALE_E2E_BLOCKED_NAME to an exact app_name_blocklist entry (see tests/journeys/README.md)")
	}
	t.Parallel()
	blocked := tg.blockedName
	tg.acquireAppSlot(t)
	var mine string
	t.Cleanup(func() {
		defer func() { <-tg.appSlots }()
		tg.removeApp(t, tg.token, mine)
	})
	s := newStory(t)
	if !s.step("deploy_gets_suffixed_name", func(t *testing.T) {
		mine = tg.createOnly(t, tg.token, blocked)
		suffixedSlug(t, blocked, mine)
	}) {
		return
	}
	s.check("notice_says_not_available", func(t *testing.T) {
		res := tg.execInEnv(t, tg.token, t.TempDir(), "", textMode, 2*time.Minute, "deploy", "--create-only", "--app", "--name", blocked)
		if res.exit != 0 {
			t.Fatalf("second deploy --name %s failed (exit %d): %s", blocked, res.exit, tg.redact(tail(string(res.stderr), 500)))
		}
		stderr := string(res.stderr)
		if !strings.Contains(stderr, "is not available") || !strings.Contains(stderr, mine) {
			t.Fatalf("second deploy did not say %s is not available and the app is %s: %s", blocked, mine, tail(stderr, 500))
		}
	})
	s.check("rename_onto_blocked_name_refused", func(t *testing.T) {
		res := tg.run(t, time.Minute, "app", mine, "rename", blocked)
		if res.exit == 0 || res.problemCode() != "app_name_unavailable" {
			t.Fatalf("rename %s -> %s: exit %d, problem %s; want app_name_unavailable", mine, blocked, res.exit, res.problem())
		}
	})
}
