import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import handler


class ReportTests(unittest.TestCase):
    def report(self, extracted, title="<script>bad()</script>"):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        inputs, outputs = root / "input", root / "output"
        inputs.mkdir()
        outputs.mkdir()
        (inputs / "input.pdf").write_bytes(b"fixture")

        def extract(arguments, **options):
            self.assertEqual(arguments[:3], ["pdftotext", "-enc", "UTF-8"])
            self.assertEqual(options["timeout"], 30)
            self.assertTrue(options["check"])
            Path(arguments[-1]).write_text(extracted, encoding="utf-8")

        with patch.object(handler.subprocess, "run", side_effect=extract):
            result = handler.main({"title": title}, {"input_dir": str(inputs), "output_dir": str(outputs)})
        return result, outputs

    def test_escapes_pdf_and_title_and_bounds_preview(self):
        result, outputs = self.report("<script>PDF</script> " + "word " * 4000 + "\f")
        report = (outputs / "report.html").read_text()
        self.assertNotIn("<script>", report)
        self.assertIn("&lt;script&gt;", report)
        self.assertEqual(result, {"pages": 1, "words": 4001, "preview_truncated": True})
        self.assertFalse((outputs / "extracted.txt").exists())

    def test_rejects_oversized_extraction(self):
        with self.assertRaisesRegex(ValueError, "report limit"):
            self.report("x" * ((1 << 20) + 1))


if __name__ == "__main__":
    unittest.main()
