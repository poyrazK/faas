from typing import Literal

CommitRoutingVersion = Literal[2]

COMMIT_ROUTING_VERSION_VALUES: set[CommitRoutingVersion] = {
    2,
}


def check_commit_routing_version(value: int) -> CommitRoutingVersion:
    if value in COMMIT_ROUTING_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {COMMIT_ROUTING_VERSION_VALUES!r}")
