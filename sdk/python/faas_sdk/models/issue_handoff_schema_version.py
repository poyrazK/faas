from typing import Literal

IssueHandoffSchemaVersion = Literal[1]

ISSUE_HANDOFF_SCHEMA_VERSION_VALUES: set[IssueHandoffSchemaVersion] = {
    1,
}


def check_issue_handoff_schema_version(value: int) -> IssueHandoffSchemaVersion:
    if value in ISSUE_HANDOFF_SCHEMA_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_HANDOFF_SCHEMA_VERSION_VALUES!r}")
