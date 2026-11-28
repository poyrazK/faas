from typing import Literal

ToolPublicationVerificationStatus = Literal[
    "approved", "completed", "failed", "not_started", "outcome_unknown", "pending", "revoked", "running"
]

TOOL_PUBLICATION_VERIFICATION_STATUS_VALUES: set[ToolPublicationVerificationStatus] = {
    "approved",
    "completed",
    "failed",
    "not_started",
    "outcome_unknown",
    "pending",
    "revoked",
    "running",
}


def check_tool_publication_verification_status(value: str) -> ToolPublicationVerificationStatus:
    if value in TOOL_PUBLICATION_VERIFICATION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TOOL_PUBLICATION_VERIFICATION_STATUS_VALUES!r}")
