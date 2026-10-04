from typing import Literal

GlobalImageReleaseTargetResponseOutcome = Literal["accepted", "blocked", "expired", "pending"]

GLOBAL_IMAGE_RELEASE_TARGET_RESPONSE_OUTCOME_VALUES: set[GlobalImageReleaseTargetResponseOutcome] = {
    "accepted",
    "blocked",
    "expired",
    "pending",
}


def check_global_image_release_target_response_outcome(value: str) -> GlobalImageReleaseTargetResponseOutcome:
    if value in GLOBAL_IMAGE_RELEASE_TARGET_RESPONSE_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {GLOBAL_IMAGE_RELEASE_TARGET_RESPONSE_OUTCOME_VALUES!r}"
    )
