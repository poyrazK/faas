import unittest
from datetime import datetime, timezone

from migrations_land import plan_renames


NOW = datetime(2026, 11, 15, 14, 0, 0, tzinfo=timezone.utc)


class PlanRenamesTest(unittest.TestCase):
    def test_branch_already_last_is_left_alone(self):
        self.assertEqual(plan_renames(
            ["20261115100000100_mine.sql"],
            ["20261114000000000_theirs.sql"], NOW), {})

    def test_older_branch_migrations_move_after_main_keeping_order(self):
        got = plan_renames(
            ["20261113000000200_b.sql", "20261113000000100_a.sql"],
            ["20261114000000000_theirs.sql", "20261113000000150_mid.sql"], NOW)
        self.assertEqual(got, {
            "20261113000000100_a.sql": "20261115140000100_a.sql",
            "20261113000000200_b.sql": "20261115140000200_b.sql",
        })

    def test_main_ahead_of_the_clock_still_sorts_after(self):
        got = plan_renames(["20261113000000100_a.sql"], ["20261116000000000_future.sql"], NOW, jitter=lambda: 61)
        self.assertEqual(got, {"20261113000000100_a.sql": "20261116000101100_a.sql"})

    def test_main_ahead_of_the_clock_spreads_branches(self):
        # Two branches landed while main is ahead of the clock must not get
        # the same version (the +1 s rule gave both 20261116000001100).
        seen = {plan_renames(["20261113000000100_a.sql"], ["20261116000000000_future.sql"], NOW)["20261113000000100_a.sql"]
                for _ in range(20)}
        self.assertGreater(len(seen), 1)
        for name in seen:
            self.assertGreater(name[:17], "20261116000000000")
            self.assertLessEqual(name[:14], "20261116010000")

    def test_non_migration_names_are_ignored(self):
        self.assertEqual(plan_renames(["README.md"], ["20261114000000000_x.sql"], NOW), {})


if __name__ == "__main__":
    unittest.main()
