from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="EnvironmentGitOpsChange")


@_attrs_define
class EnvironmentGitOpsChange:
    """One ownership or intent difference in the deterministic plan."""

    resource: str
    path: str
    action: str
    before: Any | Unset = UNSET
    after: Any | Unset = UNSET
    reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        resource = self.resource

        path = self.path

        action = self.action

        before = self.before

        after = self.after

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "resource": resource,
                "path": path,
                "action": action,
            }
        )
        if before is not UNSET:
            field_dict["before"] = before
        if after is not UNSET:
            field_dict["after"] = after
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        resource = d.pop("resource")

        path = d.pop("path")

        action = d.pop("action")

        before = d.pop("before", UNSET)

        after = d.pop("after", UNSET)

        reason = d.pop("reason", UNSET)

        environment_git_ops_change = cls(
            resource=resource,
            path=path,
            action=action,
            before=before,
            after=after,
            reason=reason,
        )

        environment_git_ops_change.additional_properties = d
        return environment_git_ops_change

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
