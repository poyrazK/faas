from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.project_environment_queue_binding import ProjectEnvironmentQueueBinding


T = TypeVar("T", bound="ReplaceProjectEnvironmentQueueBindingsRequest")


@_attrs_define
class ReplaceProjectEnvironmentQueueBindingsRequest:
    """Complete stage queue replacement fenced by the workload revision, not the queue collection clock."""

    expected_revision: int
    bindings: list[ProjectEnvironmentQueueBinding]

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

        bindings = []
        for bindings_item_data in self.bindings:
            bindings_item = bindings_item_data.to_dict()
            bindings.append(bindings_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_revision": expected_revision,
                "bindings": bindings,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_queue_binding import ProjectEnvironmentQueueBinding

        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        bindings = []
        _bindings = d.pop("bindings")
        for bindings_item_data in _bindings:
            bindings_item = ProjectEnvironmentQueueBinding.from_dict(bindings_item_data)

            bindings.append(bindings_item)

        replace_project_environment_queue_bindings_request = cls(
            expected_revision=expected_revision,
            bindings=bindings,
        )

        return replace_project_environment_queue_bindings_request
