from typing import Literal

IssueOwnershipRuleSourceKind = Literal["exception", "http", "runtime", "worker"]

ISSUE_OWNERSHIP_RULE_SOURCE_KIND_VALUES: set[IssueOwnershipRuleSourceKind] = {
    "exception",
    "http",
    "runtime",
    "worker",
}


def check_issue_ownership_rule_source_kind(value: str) -> IssueOwnershipRuleSourceKind:
    if value in ISSUE_OWNERSHIP_RULE_SOURCE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_OWNERSHIP_RULE_SOURCE_KIND_VALUES!r}")
