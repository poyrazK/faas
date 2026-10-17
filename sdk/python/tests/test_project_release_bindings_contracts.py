import unittest

from faas_sdk.api.projects import activate_checked_project_release
from faas_sdk.models.checked_project_release_manifest import CheckedProjectReleaseManifest


class CheckedReleaseContracts(unittest.TestCase):
    def test_empty_cas_and_nested_service_targets_survive_round_trip(self):
        data = {
            "project": "shop",
            "environment": "production",
            "ttl_seconds": 1800,
            "deployments": {"api": "11111111-1111-4111-8111-111111111111"},
            "expected_active_release_id": "",
            "expected_fallback_deployments": {},
            "expected_policy_revisions": {"api": 1},
            "service_targets": {"api": {}},
        }
        manifest = CheckedProjectReleaseManifest.from_dict(data)
        self.assertEqual(manifest.to_dict(), data)
        request = activate_checked_project_release._get_kwargs("shop", "production", body=manifest)
        self.assertEqual(request["url"], "/v1/projects/shop/environments/production/release-sets/activate")
        self.assertEqual(request["method"], "post")
        self.assertEqual(request["json"], data)

    def test_rollback_round_trip_keeps_historical_and_current_release(self):
        from faas_sdk.api.projects import rollback_checked_project_release
        from faas_sdk.models.project_release_set_response import ProjectReleaseSetResponse

        data = {
            "project": "shop",
            "environment": "production",
            "ttl_seconds": 1800,
            "rollback_release_id": "11111111-1111-4111-8111-111111111111",
            "deployments": {"api": "33333333-3333-4333-8333-333333333333"},
            "expected_active_release_id": "22222222-2222-4222-8222-222222222222",
            "expected_fallback_deployments": {},
            "expected_policy_revisions": {"api": 1},
            "service_targets": {"api": {}},
        }
        manifest = CheckedProjectReleaseManifest.from_dict(data)
        self.assertEqual(manifest.to_dict(), data)
        request = rollback_checked_project_release._get_kwargs("shop", "production", body=manifest)
        self.assertEqual(request["url"], "/v1/projects/shop/environments/production/release-sets/rollback")
        self.assertEqual(request["json"], data)
        receipt = ProjectReleaseSetResponse.from_dict(
            {
                "id": "44444444-4444-4444-8444-444444444444",
                "account_id": "55555555-5555-4555-8555-555555555555",
                "project_id": "66666666-6666-4666-8666-666666666666",
                "environment": "production",
                "active": True,
                "ttl_seconds": 1800,
                "created_at": "2026-10-13T00:00:00Z",
                "members": [],
                "rollback_source_release_id": data["rollback_release_id"],
                "rollback_previous_release_id": data["expected_active_release_id"],
            }
        )
        self.assertEqual(str(receipt.rollback_source_release_id), data["rollback_release_id"])
        self.assertEqual(str(receipt.rollback_previous_release_id), data["expected_active_release_id"])

    def test_deactivation_round_trip_keeps_zero_ttl_and_fallback(self):
        from faas_sdk.api.projects import deactivate_checked_project_release
        from faas_sdk.models.project_release_deactivation_receipt import ProjectReleaseDeactivationReceipt

        data = {
            "deactivate": True,
            "project": "shop",
            "environment": "production",
            "ttl_seconds": 0,
            "deployments": {"api": "22222222-2222-4222-8222-222222222222"},
            "expected_active_release_id": "11111111-1111-4111-8111-111111111111",
            "expected_fallback_deployments": {"api": "22222222-2222-4222-8222-222222222222"},
            "expected_policy_revisions": {"api": 1},
            "service_targets": {"api": {}},
        }
        manifest = CheckedProjectReleaseManifest.from_dict(data)
        self.assertEqual(manifest.to_dict(), data)
        request = deactivate_checked_project_release._get_kwargs("shop", "production", body=manifest)
        self.assertEqual(request["url"], "/v1/projects/shop/environments/production/release-sets/deactivate")
        self.assertEqual(request["json"], data)
        receipt = ProjectReleaseDeactivationReceipt.from_dict(
            {
                "id": "33333333-3333-4333-8333-333333333333",
                "release_id": data["expected_active_release_id"],
                "completed_at": "2026-10-13T00:00:00Z",
                "fallback_members": [
                    {
                        "app_id": "44444444-4444-4444-8444-444444444444",
                        "deployment_id": data["deployments"]["api"],
                    }
                ],
            }
        )
        self.assertEqual(str(receipt.release_id), data["expected_active_release_id"])
        self.assertEqual(str(receipt.fallback_members[0].deployment_id), data["deployments"]["api"])

    def test_staging_keeps_exact_manifest_and_explicit_lifecycle(self):
        from faas_sdk.api.projects import (
            cancel_project_release_stage,
            get_project_release_stage,
            stage_checked_project_release,
        )
        from faas_sdk.models.project_release_stage import ProjectReleaseStage
        from faas_sdk.models.stage_project_release_request import StageProjectReleaseRequest

        data = {
            "manifest": {
                "project": "shop",
                "environment": "production",
                "ttl_seconds": 1800,
                "deployments": {"api": "11111111-1111-4111-8111-111111111111"},
                "expected_active_release_id": "",
                "expected_fallback_deployments": {},
                "expected_policy_revisions": {"api": 1},
                "service_targets": {"api": {}},
            },
            "ttl_seconds": 60,
        }
        body = StageProjectReleaseRequest.from_dict(data)
        request = stage_checked_project_release._get_kwargs("shop", "production", body=body)
        self.assertEqual(request["json"], data)
        self.assertEqual(request["method"], "post")
        self.assertEqual(request["url"], "/v1/projects/shop/environments/production/release-sets/staging")
        stage_id = "22222222-2222-4222-8222-222222222222"
        for module, method in [(get_project_release_stage, "get"), (cancel_project_release_stage, "delete")]:
            request = module._get_kwargs("shop", "production", stage_id)
            self.assertEqual(request["method"], method)
            self.assertTrue(request["url"].endswith("/staging/" + stage_id))
        stage = ProjectReleaseStage.from_dict(
            {
                "id": stage_id,
                "project": "shop",
                "environment": "production",
                "status": "blocked",
                "reason": "capacity",
                "created_at": "2026-10-13T00:00:00Z",
                "expires_at": "2026-10-13T00:01:00Z",
                "members": [],
            }
        )
        self.assertEqual(stage.to_dict()["id"], stage_id)
        self.assertEqual(stage.to_dict()["reason"], "capacity")
