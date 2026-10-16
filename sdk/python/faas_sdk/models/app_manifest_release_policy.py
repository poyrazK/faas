from typing import Literal

AppManifestReleasePolicy = Literal["fast", "immediate", "safe"]

APP_MANIFEST_RELEASE_POLICY_VALUES: set[AppManifestReleasePolicy] = {
    "fast",
    "immediate",
    "safe",
}


def check_app_manifest_release_policy(value: str) -> AppManifestReleasePolicy:
    if value in APP_MANIFEST_RELEASE_POLICY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_MANIFEST_RELEASE_POLICY_VALUES!r}")
