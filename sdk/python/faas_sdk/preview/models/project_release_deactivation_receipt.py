from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.project_release_set_member_response import ProjectReleaseSetMemberResponse


T = TypeVar("T", bound="ProjectReleaseDeactivationReceipt")


@_attrs_define
class ProjectReleaseDeactivationReceipt:
    """Recorded deactivation decision and exact fallback routes reviewed for a project graph."""

    id: UUID
    release_id: UUID
    completed_at: datetime.datetime
    fallback_members: list[ProjectReleaseSetMemberResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        release_id = str(self.release_id)

        completed_at = self.completed_at.isoformat()

        fallback_members = []
        for fallback_members_item_data in self.fallback_members:
            fallback_members_item = fallback_members_item_data.to_dict()
            fallback_members.append(fallback_members_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "release_id": release_id,
                "completed_at": completed_at,
                "fallback_members": fallback_members,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_release_set_member_response import ProjectReleaseSetMemberResponse

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        release_id = UUID(d.pop("release_id"))

        completed_at = datetime.datetime.fromisoformat(d.pop("completed_at"))

        fallback_members = []
        _fallback_members = d.pop("fallback_members")
        for fallback_members_item_data in _fallback_members:
            fallback_members_item = ProjectReleaseSetMemberResponse.from_dict(fallback_members_item_data)

            fallback_members.append(fallback_members_item)

        project_release_deactivation_receipt = cls(
            id=id,
            release_id=release_id,
            completed_at=completed_at,
            fallback_members=fallback_members,
        )

        project_release_deactivation_receipt.additional_properties = d
        return project_release_deactivation_receipt

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
