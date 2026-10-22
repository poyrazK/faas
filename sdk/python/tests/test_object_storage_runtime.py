import unittest

from faas_sdk.object_storage_runtime import ObjectStorageBinding, ObjectStorageBindingError


def values():
    return {
        "ASSETS_ENDPOINT": "https://s3.gregale.test",
        "ASSETS_REGION": "auto",
        "ASSETS_BUCKET": "assets",
        "ASSETS_ACCESS_KEY_ID": "private-access",
        "ASSETS_SECRET_ACCESS_KEY": "private-secret",
        "ASSETS_ADDRESSING_STYLE": "path",
    }


class ObjectStorageRuntimeTest(unittest.TestCase):
    def test_rotation_revocation_and_safe_errors(self):
        def factory(config):
            self.assertEqual(config.addressing_style, "path")
            self.assertEqual(config.request_checksum_calculation, "when_required")
            self.assertEqual(config.response_checksum_validation, "when_required")
            self.assertEqual(config.region, "auto")
            self.assertNotIn("private-", repr(config))
            if config.secret_access_key == "private-rejected":
                raise ValueError("private-driver-error")
            return {"key": config.secret_access_key}

        binding = ObjectStorageBinding("ASSETS", factory)
        with self.assertRaises(ObjectStorageBindingError):
            binding.current()
        binding.reload(values())
        old, bucket = binding.current()
        self.assertEqual(bucket, "assets")
        binding.reload({**values(), "ASSETS_SECRET_ACCESS_KEY": "private-new"})
        self.assertEqual(binding.current()[0]["key"], "private-new")
        self.assertEqual(old["key"], "private-secret")
        with self.assertRaises(ObjectStorageBindingError) as error:
            binding.reload({**values(), "ASSETS_SECRET_ACCESS_KEY": "private-rejected"})
        self.assertNotIn("private-", str(error.exception))
        self.assertTrue(error.exception.__suppress_context__)
        with self.assertRaises(ObjectStorageBindingError):
            binding.current()
        with self.assertRaises(ObjectStorageBindingError):
            binding.reload({})

    def test_invalid_inputs_before_factory(self):
        for key, value in (
            ("ASSETS_ENDPOINT", "https://user:private-secret@example.test"),
            ("ASSETS_ENDPOINT", "https://example.test?secret=x"),
            ("ASSETS_BUCKET", "../other"),
            ("ASSETS_REGION", "us east"),
            ("ASSETS_ADDRESSING_STYLE", "virtual"),
        ):
            binding = ObjectStorageBinding("ASSETS", lambda config: self.fail("invalid configuration reached factory"))
            with self.assertRaises(ObjectStorageBindingError):
                binding.reload({**values(), key: value})
