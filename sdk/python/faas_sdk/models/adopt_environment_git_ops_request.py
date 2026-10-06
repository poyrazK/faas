from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="AdoptEnvironmentGitOpsRequest")


@_attrs_define
class AdoptEnvironmentGitOpsRequest:
    """Hash of the reviewed adoption plan; stale observations are rejected."""

    plan_hash: str

    def to_dict(self) -> dict[str, Any]:
        plan_hash = self.plan_hash

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "plan_hash": plan_hash,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        plan_hash = d.pop("plan_hash")

        adopt_environment_git_ops_request = cls(
            plan_hash=plan_hash,
        )

        return adopt_environment_git_ops_request
