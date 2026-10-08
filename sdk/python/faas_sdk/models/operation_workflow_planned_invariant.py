from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_business_invariant import OperationBusinessInvariant


T = TypeVar("T", bound="OperationWorkflowPlannedInvariant")


@_attrs_define
class OperationWorkflowPlannedInvariant:
    milestone: str
    invariant: OperationBusinessInvariant
    """Application-evaluated business condition. String limits are UTF-8 bytes; instance, version, and description
    must be nonempty without control characters."""

    def to_dict(self) -> dict[str, Any]:
        milestone = self.milestone

        invariant = self.invariant.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "milestone": milestone,
                "invariant": invariant,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_business_invariant import OperationBusinessInvariant

        d = dict(src_dict)
        milestone = d.pop("milestone")

        invariant = OperationBusinessInvariant.from_dict(d.pop("invariant"))

        operation_workflow_planned_invariant = cls(
            milestone=milestone,
            invariant=invariant,
        )

        return operation_workflow_planned_invariant
