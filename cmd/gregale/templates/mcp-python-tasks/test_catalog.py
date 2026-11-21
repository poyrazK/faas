import unittest

from catalog import RuntimeCatalog
from server import runtime_catalog_health


class RuntimeCatalogTests(unittest.TestCase):
    def setUp(self):
        self.catalog = RuntimeCatalog()

    def test_tool_lifecycle_and_change_notifications(self):
        changes = []
        unsubscribe = self.catalog.subscribe(lambda: changes.append("changed"))
        handler = lambda _arguments, _caller: {"content": [{"type": "text", "text": "ok"}]}
        first = {"name": "lookup", "description": "First", "inputSchema": {"type": "object"}}

        self.catalog.register("tool", first, handler)
        first["description"] = "mutated outside the catalog"
        self.assertEqual(self.catalog.list("tool")[0]["description"], "First")
        self.catalog.register("tool", {"name": "private", "inputSchema": {}}, handler)
        self.catalog.register("tool", {"name": "lookup", "description": "Updated", "inputSchema": {}}, handler, replace_name="lookup")
        self.assertEqual(self.catalog.get("tool", "lookup")["definition"]["description"], "Updated")
        self.assertTrue(self.catalog.remove("tool", "lookup"))
        self.assertFalse(self.catalog.remove("tool", "lookup"))
        self.assertEqual(len(changes), 4)
        unsubscribe()

    def test_resource_template_matches_single_uri_segments(self):
        handler = lambda _uri, _variables, _caller: []
        self.catalog.register("resource", {
            "name": "item",
            "uriTemplate": "catalog://items/{itemID}",
            "mimeType": "application/json",
        }, handler)
        self.assertEqual(self.catalog.match_resource("catalog://items/abc")["variables"], {"itemID": "abc"})
        self.assertIsNone(self.catalog.match_resource("catalog://items/a/b"))
        with self.assertRaises(ValueError):
            self.catalog.register("resource", {"name": "bad", "uri": "relative"}, handler)

    def test_contract_fingerprint_is_stable_and_revisioned(self):
        left = RuntimeCatalog()
        right = RuntimeCatalog()
        handler = lambda _arguments, _caller: {"content": []}
        first = {"name": "lookup", "description": "Lookup", "inputSchema": {"type": "object"}}
        second = {"inputSchema": {"type": "object"}, "description": "Lookup", "name": "lookup"}
        self.assertEqual(left.contract_fingerprint()[0], 0)
        left.register("tool", first, handler)
        right.register("tool", second, handler)
        left_revision, left_digest = left.contract_fingerprint()
        right_revision, right_digest = right.contract_fingerprint()
        self.assertEqual(left_revision, 1)
        self.assertEqual(right_revision, 1)
        self.assertEqual(left_digest, right_digest)
        self.assertEqual(len(left_digest), 64)
        self.assertTrue(left.remove("tool", "lookup"))
        removed_revision, removed_digest = left.contract_fingerprint()
        self.assertEqual(removed_revision, 2)
        self.assertNotEqual(removed_digest, left_digest)

    def test_runtime_health_reports_catalog_pin_mismatch(self):
        health = runtime_catalog_health("f" * 64)
        self.assertFalse(health["ready"])
        self.assertEqual(health["status"], "degraded")
        self.assertEqual(len(health["catalog_sha256"]), 64)


if __name__ == "__main__":
    unittest.main()
