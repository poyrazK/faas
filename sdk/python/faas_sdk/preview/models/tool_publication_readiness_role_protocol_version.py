from typing import Literal

ToolPublicationReadinessRoleProtocolVersion = Literal["2025-11-25", "2026-07-28"]

TOOL_PUBLICATION_READINESS_ROLE_PROTOCOL_VERSION_VALUES: set[ToolPublicationReadinessRoleProtocolVersion] = {
    "2025-11-25",
    "2026-07-28",
}


def check_tool_publication_readiness_role_protocol_version(value: str) -> ToolPublicationReadinessRoleProtocolVersion:
    if value in TOOL_PUBLICATION_READINESS_ROLE_PROTOCOL_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {TOOL_PUBLICATION_READINESS_ROLE_PROTOCOL_VERSION_VALUES!r}"
    )
