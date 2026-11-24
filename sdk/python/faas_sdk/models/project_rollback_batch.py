from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.project_rollback_member import ProjectRollbackMember


T = TypeVar("T", bound="ProjectRollbackBatch")


@_attrs_define
class ProjectRollbackBatch:
    """Recorded production project deployment membership and serving predecessors."""

    id: UUID
    members: list[ProjectRollbackMember]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        members = []
        for members_item_data in self.members:
            members_item = members_item_data.to_dict()
            members.append(members_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "members": members,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_rollback_member import ProjectRollbackMember

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        members = []
        _members = d.pop("members")
        for members_item_data in _members:
            members_item = ProjectRollbackMember.from_dict(members_item_data)

            members.append(members_item)

        project_rollback_batch = cls(
            id=id,
            members=members,
        )

        project_rollback_batch.additional_properties = d
        return project_rollback_batch

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
