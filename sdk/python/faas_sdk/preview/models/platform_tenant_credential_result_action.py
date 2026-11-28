from typing import Literal

PlatformTenantCredentialResultAction = Literal["create", "revoke", "unchanged"]

PLATFORM_TENANT_CREDENTIAL_RESULT_ACTION_VALUES: set[PlatformTenantCredentialResultAction] = {
    "create",
    "revoke",
    "unchanged",
}


def check_platform_tenant_credential_result_action(value: str) -> PlatformTenantCredentialResultAction:
    if value in PLATFORM_TENANT_CREDENTIAL_RESULT_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_CREDENTIAL_RESULT_ACTION_VALUES!r}")
