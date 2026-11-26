from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.project_rollback_summary import ProjectRollbackSummary


T = TypeVar("T", bound="ProjectRollbackHistory")


@_attrs_define
class ProjectRollbackHistory:
    """Project rollback receipts ordered with any running operation first."""

    project: str
    project_id: UUID
    operations: list[ProjectRollbackSummary]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        project = self.project

        project_id = str(self.project_id)

        operations = []
        for operations_item_data in self.operations:
            operations_item = operations_item_data.to_dict()
            operations.append(operations_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "project": project,
                "project_id": project_id,
                "operations": operations,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_rollback_summary import ProjectRollbackSummary

        d = dict(src_dict)
        project = d.pop("project")

        project_id = UUID(d.pop("project_id"))

        operations = []
        _operations = d.pop("operations")
        for operations_item_data in _operations:
            operations_item = ProjectRollbackSummary.from_dict(operations_item_data)

            operations.append(operations_item)

        project_rollback_history = cls(
            project=project,
            project_id=project_id,
            operations=operations,
        )

        project_rollback_history.additional_properties = d
        return project_rollback_history

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
