from typing import Literal

SafeApplyOrgProjectProjectReleaseMode = Literal[
    "atomic",
    "atomic-config-migrate-verify",
    "atomic-config-migrate-verify-workers",
    "atomic-config-verify",
    "atomic-config-verify-workers",
    "atomic-migrate-verify",
    "atomic-migrate-verify-workers",
    "atomic-verify",
    "atomic-verify-workers",
]

SAFE_APPLY_ORG_PROJECT_PROJECT_RELEASE_MODE_VALUES: set[SafeApplyOrgProjectProjectReleaseMode] = {
    "atomic",
    "atomic-config-migrate-verify",
    "atomic-config-migrate-verify-workers",
    "atomic-config-verify",
    "atomic-config-verify-workers",
    "atomic-migrate-verify",
    "atomic-migrate-verify-workers",
    "atomic-verify",
    "atomic-verify-workers",
}


def check_safe_apply_org_project_project_release_mode(value: str) -> SafeApplyOrgProjectProjectReleaseMode:
    if value in SAFE_APPLY_ORG_PROJECT_PROJECT_RELEASE_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SAFE_APPLY_ORG_PROJECT_PROJECT_RELEASE_MODE_VALUES!r}"
    )
