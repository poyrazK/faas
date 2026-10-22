from typing import Literal

CommitSourceResponseDestination = Literal["event", "operation"]

COMMIT_SOURCE_RESPONSE_DESTINATION_VALUES: set[CommitSourceResponseDestination] = {
    "event",
    "operation",
}


def check_commit_source_response_destination(value: str) -> CommitSourceResponseDestination:
    if value in COMMIT_SOURCE_RESPONSE_DESTINATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {COMMIT_SOURCE_RESPONSE_DESTINATION_VALUES!r}")
