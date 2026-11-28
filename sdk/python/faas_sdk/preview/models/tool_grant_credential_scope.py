from typing import Literal

ToolGrantCredentialScope = Literal["default"]

TOOL_GRANT_CREDENTIAL_SCOPE_VALUES: set[ToolGrantCredentialScope] = {
    "default",
}


def check_tool_grant_credential_scope(value: str) -> ToolGrantCredentialScope:
    if value in TOOL_GRANT_CREDENTIAL_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TOOL_GRANT_CREDENTIAL_SCOPE_VALUES!r}")
