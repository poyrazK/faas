from faas_sdk.api.projects import preview_project_environment_promotion_secrets
from faas_sdk.models.project_environment_promotion_secret_preview_request import (
    ProjectEnvironmentPromotionSecretPreviewRequest,
)


def test_promotion_secret_preview_contains_only_version_references():
    raw = {
        "from_environment": "staging",
        "sync_config": True,
        "verify": True,
        "secrets": {"api": {"TOKEN": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}, "web": {}},
    }
    request = ProjectEnvironmentPromotionSecretPreviewRequest.from_dict(raw)
    kwargs = preview_project_environment_promotion_secrets._get_kwargs("shop", "production", body=request)
    assert kwargs["method"] == "post"
    assert kwargs["url"] == "/v1/projects/shop/environments/production/promotion-preview"
    assert kwargs["json"] == raw
    assert not kwargs.get("params")
