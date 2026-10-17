#!/usr/bin/env python3
"""Unit tests for select_importers.py (light-tier importer compile check)."""
import importlib.util
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).parent))
spec = importlib.util.spec_from_file_location(
    "select_importers", pathlib.Path(__file__).with_name("select_importers.py"))
si = importlib.util.module_from_spec(spec)
spec.loader.exec_module(si)

API = """package api

// Charge bills an invoice.
func Charge(id string) error {
	return nil
}

type Invoice struct {
	ID     string
	Amount int64
}

const (
	PlanFree  = "free"
	PlanHobby = "hobby"
)

func helper() {}

type Biller interface {
	Bill(id string) error
}

func Refund(
	id string,
) error {
	return nil
}
"""


def lines_of(source, *needles):
    rows = source.split("\n")
    return {i + 1 for i, row in enumerate(rows) for n in needles if n in row}


class TouchedNamesTest(unittest.TestCase):
    def test_body_edit_selects_nothing(self):
        self.assertEqual(si.touched_names(API, lines_of(API, "return nil")), set())

    def test_signature_change_selects_the_function(self):
        self.assertEqual(si.touched_names(API, lines_of(API, "func Charge(")), {"Charge"})

    def test_comment_edit_selects_nothing(self):
        self.assertEqual(si.touched_names(API, lines_of(API, "// Charge bills")), set())

    def test_field_change_selects_the_type_and_that_field_only(self):
        self.assertEqual(si.touched_names(API, lines_of(API, "Amount int64")), {"Invoice", "Amount"})

    def test_block_entry_change_selects_that_entry_only(self):
        self.assertEqual(si.touched_names(API, lines_of(API, "PlanHobby =")), {"PlanHobby"})

    def test_unexported_declarations_never_count(self):
        self.assertEqual(si.touched_names(API, lines_of(API, "func helper")), set())

    def test_whole_file_counts_every_exported_name(self):
        self.assertEqual(si.touched_names(API, None),
                         {"Charge", "Invoice", "ID", "Amount", "PlanFree", "PlanHobby", "Biller", "Bill", "Refund"})

    def test_hunk_lines_split_old_and_new_sides(self):
        diff = "@@ -3,0 +4,2 @@\n+a\n+b\n@@ -10,2 +12 @@\n-x\n-y\n+z\n"
        self.assertEqual(si.hunk_lines(diff), ({10, 11}, {4, 5, 12}))


class SelectTest(unittest.TestCase):
    def setUp(self):
        self.root = tempfile.mkdtemp()
        self.git("init", "-q")
        self.write("pkg/api/api.go", API)
        self.write("cmd/billing/main.go", "package main\n\nfunc main() { _ = api.Charge(\"x\") }\n")
        self.write("cmd/report/main.go", "package main\n\nfunc main() { _ = api.PlanFree }\n")
        self.write("cmd/edited/main.go", "package main\n\nfunc main() { _ = api.Charge(\"x\") }\n")
        self.write("cmd/invoices/main.go", "package main\n\nvar inv = api.Invoice{ID: \"x\"}\n")
        self.write("cmd/refunds/main.go", "package main\n\nfunc main() { _ = api.Refund(\"x\") }\n")
        self.write("cmd/biller/main.go", "package main\n\nvar _ api.Biller = impl{}\n")
        self.git("add", "-A")
        self.git("-c", "user.email=ci@example.test", "-c", "user.name=ci", "commit", "-qm", "base")
        self.pkgs = {
            "pkg/api": {"path": "example.test/pkg/api", "imports": set()},
            "cmd/billing": {"path": "example.test/cmd/billing", "imports": {"example.test/pkg/api"}},
            "cmd/report": {"path": "example.test/cmd/report", "imports": {"example.test/pkg/api"}},
            "cmd/edited": {"path": "example.test/cmd/edited", "imports": {"example.test/pkg/api"}},
            "cmd/invoices": {"path": "example.test/cmd/invoices", "imports": {"example.test/pkg/api"}},
            "cmd/refunds": {"path": "example.test/cmd/refunds", "imports": {"example.test/pkg/api"}},
            "cmd/biller": {"path": "example.test/cmd/biller", "imports": {"example.test/pkg/api"}},
        }

    def git(self, *args):
        subprocess.run(["git", *args], cwd=self.root, check=True, capture_output=True)

    def write(self, path, body):
        full = os.path.join(self.root, path)
        os.makedirs(os.path.dirname(full), exist_ok=True)
        with open(full, "w", encoding="utf-8") as fh:
            fh.write(body)

    def commit(self, changes, deletes=()):
        for path, body in changes.items():
            self.write(path, body)
        for path in deletes:
            os.remove(os.path.join(self.root, path))
        self.git("add", "-A")
        self.git("-c", "user.email=ci@example.test", "-c", "user.name=ci", "commit", "-qm", "head")
        return list(changes) + list(deletes)

    def select(self, paths):
        return si.select(paths, self.pkgs, "HEAD~1", "HEAD", root=self.root)

    def test_signature_change_selects_only_importers_that_call_it(self):
        paths = self.commit({"pkg/api/api.go": API.replace("Charge(id string)", "Charge(id string, cents int64)"),
                             "cmd/edited/main.go": "package main\n\nfunc main() { _ = api.Charge(\"x\", 1) }\n"})
        selected, names, importers = self.select(paths)
        # cmd/edited changed too, so golangci-lint already compiles it.
        self.assertEqual(selected, ["cmd/billing"])
        self.assertEqual(names, {"pkg/api": {"Charge"}})
        self.assertEqual(importers, 5)

    def test_body_edit_selects_no_importer(self):
        paths = self.commit({"pkg/api/api.go": API.replace("return nil", "return errNotFound")})
        self.assertEqual(self.select(paths)[0], [])

    def test_deleted_file_selects_every_importer_using_it(self):
        paths = self.commit({}, deletes=["pkg/api/api.go"])
        self.assertEqual(sorted(self.select(paths)[0]), ["cmd/biller", "cmd/billing", "cmd/edited", "cmd/invoices",
                                                         "cmd/refunds", "cmd/report"])

    def test_importers_using_rarer_names_come_first(self):
        # Charge is used by two importers, Refund by one: cmd/refunds leads.
        paths = self.commit({"pkg/api/api.go": API.replace("Charge(id string)", "Charge(id string, cents int64)")
                             .replace("\tid string,\n) error {", "\tid string,\n\treason string,\n) error {")})
        self.assertEqual(self.select(paths)[0], ["cmd/refunds", "cmd/billing", "cmd/edited"])

    def test_added_field_and_gofmt_realignment_select_nothing(self):
        grown = API.replace("\tID     string\n\tAmount int64\n",
                            "\tID              string\n\tAmount          int64\n\tSettlementCents int64\n")
        self.assertEqual(self.select(self.commit({"pkg/api/api.go": grown}))[0], [])

    def test_removed_field_selects_keyed_literal_users(self):
        paths = self.commit({"pkg/api/api.go": API.replace("\tAmount int64\n", "")})
        selected, names, _ = self.select(paths)
        self.assertEqual(names, {"pkg/api": {"Invoice", "Amount"}})
        self.assertEqual(selected, ["cmd/invoices"])

    def test_added_parameter_line_selects_callers(self):
        paths = self.commit({"pkg/api/api.go": API.replace("\tid string,\n) error {", "\tid string,\n\treason string,\n) error {")})
        self.assertEqual(self.select(paths)[0], ["cmd/refunds"])

    def test_added_interface_method_selects_implementations_that_assert_it(self):
        paths = self.commit({"pkg/api/api.go": API.replace("\tBill(id string) error\n", "\tBill(id string) error\n\tVoid(id string) error\n")})
        self.assertEqual(self.select(paths)[0], ["cmd/biller"])

    def test_added_file_selects_nothing(self):
        paths = self.commit({"pkg/api/new.go": "package api\n\nfunc Refund() {}\n"})
        self.assertEqual(self.select(paths)[0], [])


if __name__ == "__main__":
    unittest.main()
