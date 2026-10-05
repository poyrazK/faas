"""ADR-521: the coverage union preserves covered and uncovered state blocks."""

import unittest

from normalize_go_cover_profile import normalize


class CoverageProfileTest(unittest.TestCase):
    def test_atomic_union_counts_each_statement_once(self):
        profile = (
            "mode: atomic\n"
            "example/pkg/state/a.go:1.1,2.2 3 0\n"
            "example/pkg/state/a.go:3.1,4.2 2 0\n"
            "example/pkg/state/a.go:1.1,2.2 3 4\n"
            "example/pkg/state/a.go:1.1,2.2 3 5\n"
        )
        self.assertEqual(
            normalize(profile),
            "mode: atomic\n"
            "example/pkg/state/a.go:1.1,2.2 3 9\n"
            "example/pkg/state/a.go:3.1,4.2 2 0\n",
        )

    def test_set_mode_uses_boolean_hits(self):
        self.assertEqual(
            normalize("mode: set\na.go:1.1,2.2 1 1\na.go:1.1,2.2 1 1\n"),
            "mode: set\na.go:1.1,2.2 1 1\n",
        )

    def test_single_profile_is_stable(self):
        profile = "mode: count\na.go:1.1,2.2 1 3\nb.go:1.1,2.2 2 0\n"
        self.assertEqual(normalize(profile), profile)

    def test_rejects_conflicting_statement_counts(self):
        with self.assertRaisesRegex(ValueError, "conflicting statement count"):
            normalize("mode: atomic\na.go:1.1,2.2 1 0\na.go:1.1,2.2 2 1\n")

    def test_rejects_bad_mode_and_negative_counts(self):
        for profile in ("mode: unknown\n", "mode: atomic\na.go:1.1,2.2 1 -1\n"):
            with self.subTest(profile=profile), self.assertRaises(ValueError):
                normalize(profile)


if __name__ == "__main__":
    unittest.main()
