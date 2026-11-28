from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectRollbackUndoRequest")


@_attrs_define
class ProjectRollbackUndoRequest:
    """Review and acknowledge the inverse of a paused rollback's completed cutovers."""

    acknowledge_releases: bool | Unset = False
    """ Acknowledge release commands crossed by this undo. """
    expected_plan_hash: str | Unset = UNSET
    """ Plan hash from a runnable undo preview of the same source with the same release acknowledgement. """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        acknowledge_releases = self.acknowledge_releases

        expected_plan_hash = self.expected_plan_hash

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if acknowledge_releases is not UNSET:
            field_dict["acknowledge_releases"] = acknowledge_releases
        if expected_plan_hash is not UNSET:
            field_dict["expected_plan_hash"] = expected_plan_hash

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        acknowledge_releases = d.pop("acknowledge_releases", UNSET)

        expected_plan_hash = d.pop("expected_plan_hash", UNSET)

        project_rollback_undo_request = cls(
            acknowledge_releases=acknowledge_releases,
            expected_plan_hash=expected_plan_hash,
        )

        project_rollback_undo_request.additional_properties = d
        return project_rollback_undo_request

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
