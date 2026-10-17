from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

T = TypeVar("T", bound="UpdateIssueLifecycleAlertPolicyRequest")


@_attrs_define
class UpdateIssueLifecycleAlertPolicyRequest:
    """Revision-guarded replacement lifecycle alert policy."""

    expected_revision: int
    environments: list[str]
    """Replacement allowlist of exact names; an empty array removes the environment filter."""
    cooldown_seconds: int

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

        environments = self.environments

        cooldown_seconds = self.cooldown_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_revision": expected_revision,
                "environments": environments,
                "cooldown_seconds": cooldown_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        environments = cast(list[str], d.pop("environments"))

        cooldown_seconds = d.pop("cooldown_seconds")

        update_issue_lifecycle_alert_policy_request = cls(
            expected_revision=expected_revision,
            environments=environments,
            cooldown_seconds=cooldown_seconds,
        )

        return update_issue_lifecycle_alert_policy_request
