from typing import Literal

ToolPublicationVerificationOutcomeChecksItemStatus = Literal["failed", "passed"]

TOOL_PUBLICATION_VERIFICATION_OUTCOME_CHECKS_ITEM_STATUS_VALUES: set[
    ToolPublicationVerificationOutcomeChecksItemStatus
] = {
    "failed",
    "passed",
}


def check_tool_publication_verification_outcome_checks_item_status(
    value: str,
) -> ToolPublicationVerificationOutcomeChecksItemStatus:
    if value in TOOL_PUBLICATION_VERIFICATION_OUTCOME_CHECKS_ITEM_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {TOOL_PUBLICATION_VERIFICATION_OUTCOME_CHECKS_ITEM_STATUS_VALUES!r}"
    )
