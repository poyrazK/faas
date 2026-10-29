from faas_sdk.preview.api.projects import apply_cached_project_plan
from faas_sdk.preview.models.apply_cached_project_plan_request import ApplyCachedProjectPlanRequest
from faas_sdk.preview.models.project_initial_secrets import ProjectInitialSecrets
from faas_sdk.preview.models.project_source_ref_scan_request import ProjectSourceRefScanRequest


def test_initial_values_remain_in_the_apply_request_wire_body():
    values = ProjectInitialSecrets.from_dict({"api": {"DATABASE_URL": "request-only"}})
    body = ApplyCachedProjectPlanRequest(plan_token="reviewed", only=["api"], initial_secrets=values)
    kwargs = apply_cached_project_plan._get_kwargs(body=body)
    assert kwargs["method"] == "post"
    assert kwargs["url"] == "/v1/projects/apply-plan"
    assert kwargs["json"] == {
        "plan_token": "reviewed",
        "only": ["api"],
        "initial_secrets": {"api": {"DATABASE_URL": "request-only"}},
    }


def test_connected_scan_preserves_manual_migration_mode():
    from faas_sdk.preview.models.plan_workload import PlanWorkload
    from faas_sdk.preview.models.project_source_ref_scan_request_app_slugs import ProjectSourceRefScanRequestAppSlugs

    request = ProjectSourceRefScanRequest(
        repo="team/shop",
        ref="main",
        project_slug="shop",
        migration=True,
        app_slugs=ProjectSourceRefScanRequestAppSlugs.from_dict({"api": "shop-backend"}),
    )
    assert request.to_dict()["migration"] is True
    assert request.to_dict()["app_slugs"] == {"api": "shop-backend"}
    workload = PlanWorkload.from_dict(
        {"name": "api", "app_slug": "shop-backend", "root_dir": "api", "command": [], "ports": []}
    )
    assert workload.name == "api"
    assert workload.app_slug == "shop-backend"


def test_durable_project_migration_create_and_resume_wire_identity():
    from uuid import UUID

    from faas_sdk.preview.api.projects import create_project_migration, resume_project_migration
    from faas_sdk.preview.models import CreateProjectMigrationRequestBuildDependencies, ProjectPublicBuildURLs
    from faas_sdk.preview.models.create_project_migration_request import CreateProjectMigrationRequest
    from faas_sdk.preview.models.project_service_connections import ProjectServiceConnections
    from faas_sdk.preview.models.resume_project_migration_request import ResumeProjectMigrationRequest

    migration = UUID("7a7ab9c4-2995-48d5-85c5-c094ce55c599")
    body = CreateProjectMigrationRequest(
        id=migration,
        plan_token="reviewed",
        only=["api", "web"],
        initial_secrets=ProjectInitialSecrets.from_dict({"api": {"DATABASE_URL": "request-only"}}),
        service_connections=ProjectServiceConnections.from_dict({"web": {"API_URL": "api"}}),
        build_dependencies=CreateProjectMigrationRequestBuildDependencies.from_dict({"web": ["api"]}),
        public_service_connections=ProjectServiceConnections.from_dict({"web": {"VITE_API_URL": "api"}}),
        public_build_urls=ProjectPublicBuildURLs.from_dict(
            {"web": {"NEXT_PUBLIC_AUTH_URL": "https://auth.example.com/v1"}}
        ),
    )
    create = create_project_migration._get_kwargs(body=body)
    assert create["url"] == "/v1/project-migrations"
    assert create["json"]["id"] == str(migration)
    assert create["json"]["only"] == ["api", "web"]
    assert create["json"]["build_dependencies"] == {"web": ["api"]}
    assert create["json"]["service_connections"] == {"web": {"API_URL": "api"}}
    assert create["json"]["public_service_connections"] == {"web": {"VITE_API_URL": "api"}}
    assert create["json"]["public_build_urls"] == {"web": {"NEXT_PUBLIC_AUTH_URL": "https://auth.example.com/v1"}}
    resume = resume_project_migration._get_kwargs(
        migration=migration,
        body=ResumeProjectMigrationRequest(plan_token="same-source", expected_version=4, only=["api"]),
    )
    assert resume["url"] == f"/v1/project-migrations/{migration}/resume"
    assert resume["json"] == {"plan_token": "same-source", "expected_version": 4, "only": ["api"]}
