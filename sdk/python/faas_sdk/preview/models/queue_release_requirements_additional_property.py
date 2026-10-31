from typing import Literal

QueueReleaseRequirementsAdditionalProperty = Literal["consumer_readiness", "handler_completion"]

QUEUE_RELEASE_REQUIREMENTS_ADDITIONAL_PROPERTY_VALUES: set[QueueReleaseRequirementsAdditionalProperty] = {
    "consumer_readiness",
    "handler_completion",
}


def check_queue_release_requirements_additional_property(value: str) -> QueueReleaseRequirementsAdditionalProperty:
    if value in QUEUE_RELEASE_REQUIREMENTS_ADDITIONAL_PROPERTY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {QUEUE_RELEASE_REQUIREMENTS_ADDITIONAL_PROPERTY_VALUES!r}"
    )
