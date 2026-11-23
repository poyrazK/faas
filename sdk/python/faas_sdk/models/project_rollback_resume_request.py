from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectRollbackResumeRequest")


@_attrs_define
class ProjectRollbackResumeRequest:
    """Require a reviewed plan when resuming a paused project rollback or undo."""

    expected_plan_hash: str | Unset = UNSET
    """Resume plan hash from a runnable preview of the same paused operation."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        expected_plan_hash = self.expected_plan_hash

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if expected_plan_hash is not UNSET:
            field_dict["expected_plan_hash"] = expected_plan_hash

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_plan_hash = d.pop("expected_plan_hash", UNSET)

        project_rollback_resume_request = cls(
            expected_plan_hash=expected_plan_hash,
        )

        project_rollback_resume_request.additional_properties = d
        return project_rollback_resume_request

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
