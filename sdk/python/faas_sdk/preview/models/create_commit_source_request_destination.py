from typing import Literal

CreateCommitSourceRequestDestination = Literal["event", "operation"]

CREATE_COMMIT_SOURCE_REQUEST_DESTINATION_VALUES: set[CreateCommitSourceRequestDestination] = {
    "event",
    "operation",
}


def check_create_commit_source_request_destination(value: str) -> CreateCommitSourceRequestDestination:
    if value in CREATE_COMMIT_SOURCE_REQUEST_DESTINATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_COMMIT_SOURCE_REQUEST_DESTINATION_VALUES!r}")
