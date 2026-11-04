from typing import Literal

AppManifestRestartPolicyType3Type1 = Literal["always", "no", "on-failure", "unless-stopped"]

APP_MANIFEST_RESTART_POLICY_TYPE_3_TYPE_1_VALUES: set[AppManifestRestartPolicyType3Type1] = {
    "always",
    "no",
    "on-failure",
    "unless-stopped",
}


def check_app_manifest_restart_policy_type_3_type_1(value: str) -> AppManifestRestartPolicyType3Type1:
    if value in APP_MANIFEST_RESTART_POLICY_TYPE_3_TYPE_1_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_MANIFEST_RESTART_POLICY_TYPE_3_TYPE_1_VALUES!r}")
