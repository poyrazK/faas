from typing import Literal

ProjectEnvironmentPromotionChangeSourceRevisionKind = Literal[
    "commit_sha", "deployment_id", "image_digest", "source_sha256"
]

PROJECT_ENVIRONMENT_PROMOTION_CHANGE_SOURCE_REVISION_KIND_VALUES: set[
    ProjectEnvironmentPromotionChangeSourceRevisionKind
] = {
    "commit_sha",
    "deployment_id",
    "image_digest",
    "source_sha256",
}


def check_project_environment_promotion_change_source_revision_kind(
    value: str,
) -> ProjectEnvironmentPromotionChangeSourceRevisionKind:
    if value in PROJECT_ENVIRONMENT_PROMOTION_CHANGE_SOURCE_REVISION_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_PROMOTION_CHANGE_SOURCE_REVISION_KIND_VALUES!r}"
    )
