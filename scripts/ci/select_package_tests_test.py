#!/usr/bin/env python3
"""Unit tests for select_package_tests.py (light-tier selective tests)."""
import importlib.util
import os
import pathlib
import tempfile
import unittest

spec = importlib.util.spec_from_file_location(
    "select_package_tests", pathlib.Path(__file__).with_name("select_package_tests.py"))
st = importlib.util.module_from_spec(spec)
spec.loader.exec_module(st)

PKG = "pkg/big"


class SelectTest(unittest.TestCase):
    def setUp(self):
        self.root = tempfile.mkdtemp()
        os.makedirs(os.path.join(self.root, PKG))
        files = {
            "billing.go": "package big\n\nfunc ChargeInvoice() {}\n\ntype InvoiceLedger struct{}\n",
            "billing_test.go": "package big\n\nfunc TestChargeInvoice(t *testing.T) {}\n",
            "billing_credits_test.go": "package big\n\nfunc TestCredits(t *testing.T) {}\n",
            "reports.go": "package big\n\nfunc RenderUsageReport() {}\n",
            "reports_test.go": "package big\n\nfunc TestRenderUsageReport(t *testing.T) {}\n",
            "ledger_view_test.go": "package big\n\nfunc TestLedgerView(t *testing.T) { _ = InvoiceLedger{} }\n",
            "unrelated_test.go": "package big\n\nfunc TestUnrelated(t *testing.T) {}\n",
        }
        for name, body in files.items():
            with open(os.path.join(self.root, PKG, name), "w", encoding="utf-8") as fh:
                fh.write(body)

    def select(self, paths, cap=150):
        return st.select(PKG, paths, cap, root=self.root)

    def test_unrelated_change_selects_nothing(self):
        selected, total, _, _ = self.select(["docs/readme.md", "pkg/other/x.go"])
        self.assertEqual(selected, [])
        self.assertEqual(total, 5)

    def test_source_change_selects_named_and_referencing_tests(self):
        selected, _, reasons, _ = self.select([PKG + "/billing.go"])
        # billing_test.go and billing_credits_test.go by file name, then the
        # test that mentions the changed InvoiceLedger type.
        self.assertEqual(selected, ["TestChargeInvoice", "TestCredits", "TestLedgerView"])
        self.assertEqual(reasons["file-name match"], 2)
        self.assertEqual(reasons["identifier match"], 1)

    def test_changed_test_file_comes_first_and_cap_keeps_priority(self):
        selected, _, _, candidates = self.select([PKG + "/billing.go", PKG + "/unrelated_test.go"], cap=2)
        self.assertEqual(selected, ["TestUnrelated", "TestChargeInvoice"])
        self.assertEqual(candidates, 4)

    def test_generic_stem_does_not_select_every_prefixed_file(self):
        for i in range(st.MAX_STEM_PREFIX_FILES + 1):
            with open(os.path.join(self.root, PKG, f"reports_part{i}_test.go"), "w", encoding="utf-8") as fh:
                fh.write(f"package big\n\nfunc TestPart{i}(t *testing.T) {{}}\n")
        selected, _, _, _ = self.select([PKG + "/reports.go"])
        self.assertEqual(selected, ["TestRenderUsageReport"])

    def test_subpackage_files_do_not_count(self):
        selected, _, _, _ = self.select([PKG + "/sub/billing.go"])
        self.assertEqual(selected, [])

    def test_touched_source_keeps_only_changed_declarations(self):
        source = "package big\n\nfunc ChargeInvoice() {\n\treturn\n}\n\nfunc RenderUsageReport() {}\n"
        self.assertEqual(st.declared_identifiers(st.touched_source(source, {4})), {"ChargeInvoice"})
        self.assertEqual(st.declared_identifiers(st.touched_source(source, None)),
                         {"ChargeInvoice", "RenderUsageReport"})


if __name__ == "__main__":
    unittest.main()
