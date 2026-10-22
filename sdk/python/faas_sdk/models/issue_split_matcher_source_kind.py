from typing import Literal

IssueSplitMatcherSourceKind = Literal["", "exception", "http", "runtime", "worker"]

ISSUE_SPLIT_MATCHER_SOURCE_KIND_VALUES: set[IssueSplitMatcherSourceKind] = {
    "",
    "exception",
    "http",
    "runtime",
    "worker",
}


def check_issue_split_matcher_source_kind(value: str) -> IssueSplitMatcherSourceKind:
    if value in ISSUE_SPLIT_MATCHER_SOURCE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_SPLIT_MATCHER_SOURCE_KIND_VALUES!r}")
