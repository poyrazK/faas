#!/usr/bin/env python3
"""Hermetic release-pin capability checks; never consult a mutable tag."""
import importlib.util
import io
from pathlib import Path
import unittest
import urllib.error

spec = importlib.util.spec_from_file_location(
    "resolver", Path(__file__).with_name("resolve-github-action-region-support.py")
)
resolver = importlib.util.module_from_spec(spec)
spec.loader.exec_module(resolver)
SHA = "0123456789abcdef0123456789abcdef01234567"


class Response(io.BytesIO):
    status = 200

    def __init__(self, body=b"", size="123"):
        super().__init__(body)
        self.headers = {"Content-Length": size}


class RegionSupportTests(unittest.TestCase):
    def resolve(self, body, size="123"):
        calls = []

        def opener(request, timeout):
            self.assertEqual(timeout, 10)
            url = request if isinstance(request, str) else request.full_url
            self.assertIn(f"/{SHA}/.github/actions/deploy/", url)
            calls.append(url)
            if isinstance(request, str):
                return Response(body)
            self.assertEqual(request.get_method(), "HEAD")
            return Response(size=size)

        result = resolver.resolve_support(SHA.upper(), opener)
        return result, calls

    def test_released_support_is_bound_to_same_sha(self):
        result, calls = self.resolve(b'{"schema_version":1,"compute_region":true}')
        self.assertEqual(result, SHA)
        self.assertEqual(len(calls), 2)

    def test_old_action_is_unsupported(self):
        def missing(request, timeout):
            raise urllib.error.HTTPError(request, 404, "missing", {}, None)

        self.assertEqual(resolver.resolve_support(SHA, missing), "")
        result, calls = self.resolve(b'{"schema_version":1,"compute_region":false}')
        self.assertEqual(result, "")
        self.assertEqual(len(calls), 1)

    def test_invalid_manifests_fail(self):
        for body in (b"not json", b"[]", b'{"schema_version":true}',
                     b'{"schema_version":2}', b"x" * 4097):
            with self.subTest(body=body[:80]), self.assertRaises(ValueError):
                self.resolve(body)

    def test_source_commit_without_bundle_fails(self):
        for size in ("0", "-1", "bad"):
            with self.subTest(size=size), self.assertRaises(ValueError):
                self.resolve(b'{"schema_version":1,"compute_region":true}', size)

    def test_bad_pin_rejected_without_network(self):
        def unexpected(*args, **kwargs):
            self.fail("unexpected network request")

        with self.assertRaises(ValueError):
            resolver.resolve_support("v0", unexpected)

    def test_network_failure_is_not_unsupported(self):
        def offline(*args, **kwargs):
            raise urllib.error.URLError("offline")

        with self.assertRaises(urllib.error.URLError):
            resolver.resolve_support(SHA, offline)


if __name__ == "__main__":
    unittest.main()
