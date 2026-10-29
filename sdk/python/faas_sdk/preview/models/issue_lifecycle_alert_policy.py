from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

T = TypeVar("T", bound="IssueLifecycleAlertPolicy")


@_attrs_define
class IssueLifecycleAlertPolicy:
    """Native new-issue/regression environment filter and shared per-group cooldown."""

    revision: int
    environments: list[str]
    """Environments permitted by the saved policy; an empty array means all."""
    cooldown_seconds: int

    def to_dict(self) -> dict[str, Any]:
        revision = self.revision

        environments = self.environments

        cooldown_seconds = self.cooldown_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "revision": revision,
                "environments": environments,
                "cooldown_seconds": cooldown_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        revision = d.pop("revision")

        environments = cast(list[str], d.pop("environments"))

        cooldown_seconds = d.pop("cooldown_seconds")

        issue_lifecycle_alert_policy = cls(
            revision=revision,
            environments=environments,
            cooldown_seconds=cooldown_seconds,
        )

        return issue_lifecycle_alert_policy
