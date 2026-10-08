from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowTransition")


@_attrs_define
class OperationWorkflowTransition:
    """Allowed state edge declared by a pinned application workflow definition."""

    from_: str
    to: str

    def to_dict(self) -> dict[str, Any]:
        from_ = self.from_

        to = self.to

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "from": from_,
                "to": to,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        from_ = d.pop("from")

        to = d.pop("to")

        operation_workflow_transition = cls(
            from_=from_,
            to=to,
        )

        return operation_workflow_transition
