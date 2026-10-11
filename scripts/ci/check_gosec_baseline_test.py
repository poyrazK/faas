import unittest

from check_gosec_baseline import missing_baseline_rules


def config(includes, excludes=()):
    return ("    gosec:\n      includes:\n" +
            "".join("        - " + rule + "\n" for rule in includes) +
            "      excludes:\n" +
            "".join("        - " + rule + "\n" for rule in excludes) +
            "  exclusions:\n")


class SecurityBaselineTests(unittest.TestCase):
    def test_existing_documented_exclusions_are_preserved(self):
        self.assertEqual(missing_baseline_rules(config(["G101", "G107", "G201"], ["G101"]),
                                               ["G101", "G107", "G201"]), set())

    def test_removed_rule_fails(self):
        self.assertEqual(missing_baseline_rules(config(["G101", "G107"], ["G101"]),
                                               ["G101", "G107", "G201"]), {"G201"})

    def test_new_exclusion_cannot_silently_disable_an_existing_rule(self):
        self.assertEqual(missing_baseline_rules(config(["G101", "G107", "G201"], ["G101", "G107"]),
                                               ["G101", "G107", "G201"]), {"G107"})

    def test_implicit_or_missing_policy_fails_closed(self):
        for value in ["", "    gosec:\n      excludes:\n        - G101\n"]:
            with self.assertRaises(ValueError):
                missing_baseline_rules(value, ["G107"])


if __name__ == "__main__":
    unittest.main()
