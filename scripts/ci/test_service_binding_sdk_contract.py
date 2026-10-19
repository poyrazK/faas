"""Mutation checks for the schema drift gate; no API server or credentials."""

import copy
import importlib.util
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SPEC = importlib.util.spec_from_file_location(
    "binding_contract",
    Path(__file__).with_name("check-service-binding-sdk-contract.py"),
)
checker = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(checker)


class ContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.document = checker.YAML(typ="safe").load(checker.ROOT / "api/openapi.yaml")
        cls.baseline = checker.contract(cls.document)

    def changed(self, schema, field, value):
        document = copy.deepcopy(self.document)
        document["components"]["schemas"][schema]["properties"][field] = value
        return checker.contract(document)

    def test_new_binding_field_is_discovered(self):
        self.assertNotEqual(
            self.baseline,
            self.changed("AppManifest", "service_binding_future", {"type": "boolean"}),
        )

    def test_nested_type_changes_are_detected(self):
        self.assertNotEqual(
            self.baseline,
            self.changed("ServiceCallScope", "methods", {"type": "string"}),
        )

    def test_patch_nullability_is_pinned(self):
        self.assertNotEqual(
            self.baseline,
            self.changed(
                "UpdateAppRequest",
                "allowed_service_callers",
                {"type": "array", "maxItems": 100, "items": {"type": "string"}},
            ),
        )

    def test_fingerprint_constraints_are_pinned(self):
        self.assertNotEqual(
            self.baseline,
            self.changed(
                "UpdateAppRequest",
                "expected_service_binding_fingerprint",
                {"type": "string"},
            ),
        )

    def test_new_references_are_followed(self):
        document = copy.deepcopy(self.document)
        schemas = document["components"]["schemas"]
        schemas["BindingFuture"] = {
            "type": "object",
            "properties": {"enabled": {"type": "boolean"}},
        }
        schemas["AppManifest"]["properties"]["service_binding_future"] = {
            "$ref": "#/components/schemas/BindingFuture"
        }
        self.assertIn("BindingFuture", checker.contract(document)["schemas"])

    def test_prose_and_unrelated_fields_do_not_invalidate_contract(self):
        document = copy.deepcopy(self.document)
        schemas = document["components"]["schemas"]
        schemas["AppManifest"]["properties"]["unrelated"] = {"type": "string"}
        schemas["ServiceCallScope"]["properties"]["methods"]["description"] = (
            "New prose"
        )
        self.assertEqual(self.baseline, checker.contract(document))

    def test_shared_cases_cover_every_captured_field_and_named_enum(self):
        cases = json.loads(
            (checker.ROOT / "sdk/service-binding-contracts/cases.json").read_text()
        )
        self.assertEqual([], checker.missing_fixture_coverage(self.baseline, cases))
        changed = self.changed(
            "AppManifest", "service_binding_future", {"type": "boolean"}
        )
        self.assertIn(
            "AppManifest.service_binding_future",
            checker.missing_fixture_coverage(changed, cases),
        )

    def test_cli_exits_nonzero_for_schema_drift(self):
        document = {"components": {"schemas": copy.deepcopy(self.baseline["schemas"])}}
        document["components"]["schemas"]["AppManifest"]["properties"][
            "service_binding_future"
        ] = {"type": "boolean"}
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "openapi.json"
            path.write_text(json.dumps(document))
            result = subprocess.run(
                [
                    sys.executable,
                    str(
                        Path(__file__).with_name(
                            "check-service-binding-sdk-contract.py"
                        )
                    ),
                    "--openapi",
                    str(path),
                ],
                capture_output=True,
                text=True,
                check=False,
            )
        self.assertEqual(1, result.returncode, result.stderr)
        self.assertIn("Service-binding contract drift", result.stdout)
        self.assertIn("service_binding_future", result.stdout)


if __name__ == "__main__":
    unittest.main()
