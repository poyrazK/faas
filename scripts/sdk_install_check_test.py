#!/usr/bin/env python3
"""The install gate must execute a README example or fail, never skip it."""
import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("sdk_install_check", Path(__file__).with_name("sdk_install_check.py"))
check = importlib.util.module_from_spec(spec)
spec.loader.exec_module(check)

START = "<!-- sdk-install-quickstart:start -->"
END = "<!-- sdk-install-quickstart:end -->"


class QuickstartTest(unittest.TestCase):
    def extract(self, text):
        with tempfile.TemporaryDirectory() as tmp:
            package = Path(tmp)
            (package / "README.md").write_text(text)
            return check.quickstart(package)

    def test_executes_the_exact_marked_source(self):
        for language in ("js", "python", "go"):
            with self.subTest(language=language):
                code = "first()\n\nsecond()"
                readme = f"Ignored prose\n{START}\n```{language}\n{code}\n```\n{END}\nOther prose"
                self.assertEqual(self.extract(readme), code + "\n")

    def test_missing_duplicate_or_malformed_examples_fail_closed(self):
        examples = [
            "No example",
            f"{START}\n```js\nrun()\n```",
            f"{START}\n```js\nrun()\n```\n{END}\n{START}\n{END}",
            f"{START}\nrun()\n{END}",
            f"{START}\n```sh\nrun()\n```\n{END}",
        ]
        for readme in examples:
            with self.subTest(readme=readme), self.assertRaises(RuntimeError):
                self.extract(readme)


if __name__ == "__main__":
    unittest.main()
