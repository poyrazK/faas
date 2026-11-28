from typing import Literal

ProjectEnvironmentPromotionChangeTargetRevisionKind = Literal[
    "commit_sha", "deployment_id", "image_digest", "source_sha256"
]

PROJECT_ENVIRONMENT_PROMOTION_CHANGE_TARGET_REVISION_KIND_VALUES: set[
    ProjectEnvironmentPromotionChangeTargetRevisionKind
] = {
    "commit_sha",
    "deployment_id",
    "image_digest",
    "source_sha256",
}


def check_project_environment_promotion_change_target_revision_kind(
    value: str,
) -> ProjectEnvironmentPromotionChangeTargetRevisionKind:
    if value in PROJECT_ENVIRONMENT_PROMOTION_CHANGE_TARGET_REVISION_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_PROMOTION_CHANGE_TARGET_REVISION_KIND_VALUES!r}"
    )
