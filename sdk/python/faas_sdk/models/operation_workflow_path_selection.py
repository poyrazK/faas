from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowPathSelection")


@_attrs_define
class OperationWorkflowPathSelection:
    """Application-reported choice of one named path in a compound prerequisite group of the selected pinned transition.
    Each group may appear once. Choices do not authorize or execute actions.

    """

    group_id: str
    path_id: str

    def to_dict(self) -> dict[str, Any]:
        group_id = self.group_id

        path_id = self.path_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "group_id": group_id,
                "path_id": path_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        group_id = d.pop("group_id")

        path_id = d.pop("path_id")

        operation_workflow_path_selection = cls(
            group_id=group_id,
            path_id=path_id,
        )

        return operation_workflow_path_selection
