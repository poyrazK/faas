from typing import Literal

AppManifestRestartPolicyType1 = Literal["always", "no", "on-failure", "unless-stopped"]

APP_MANIFEST_RESTART_POLICY_TYPE_1_VALUES: set[AppManifestRestartPolicyType1] = {
    "always",
    "no",
    "on-failure",
    "unless-stopped",
}


def check_app_manifest_restart_policy_type_1(value: str) -> AppManifestRestartPolicyType1:
    if value in APP_MANIFEST_RESTART_POLICY_TYPE_1_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_MANIFEST_RESTART_POLICY_TYPE_1_VALUES!r}")
