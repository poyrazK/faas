from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowTransition")


@_attrs_define
class OperationWorkflowTransition:
    """Allowed state edge declared by a pinned application workflow definition."""

    from_: str
    to: str
    required_milestones: list[str] | Unset = UNSET
    """Milestone names that must be committed in the same application transaction as this transition."""

    def to_dict(self) -> dict[str, Any]:
        from_ = self.from_

        to = self.to

        required_milestones: list[str] | Unset = UNSET
        if not isinstance(self.required_milestones, Unset):
            required_milestones = self.required_milestones

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "from": from_,
                "to": to,
            }
        )
        if required_milestones is not UNSET:
            field_dict["required_milestones"] = required_milestones

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        from_ = d.pop("from")

        to = d.pop("to")

        required_milestones = cast(list[str], d.pop("required_milestones", UNSET))

        operation_workflow_transition = cls(
            from_=from_,
            to=to,
            required_milestones=required_milestones,
        )

        return operation_workflow_transition
