from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationStartRequest")


@_attrs_define
class OperationStartRequest:
    """Submission owned by the authenticated platform tenant."""

    definition_id: UUID
    input_: Any
    """JSON input matching the pinned definition."""

    def to_dict(self) -> dict[str, Any]:
        definition_id = str(self.definition_id)

        input_ = self.input_

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "definition_id": definition_id,
                "input": input_,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        definition_id = UUID(d.pop("definition_id"))

        input_ = d.pop("input")

        operation_start_request = cls(
            definition_id=definition_id,
            input_=input_,
        )

        return operation_start_request
