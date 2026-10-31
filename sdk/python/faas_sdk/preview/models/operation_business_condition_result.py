from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_business_condition_result_result import (
    OperationBusinessConditionResultResult,
    check_operation_business_condition_result_result,
)

T = TypeVar("T", bound="OperationBusinessConditionResult")


@_attrs_define
class OperationBusinessConditionResult:
    """Application-reported condition result matching the pinned milestone declaration. Omitted conditions remain
    unreported; unknown is explicit. No execution authority or causal inference.

    """

    id: str
    description: str
    """Exact public description from the pinned milestone declaration; UTF-8 byte bound."""
    result: OperationBusinessConditionResultResult

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        description = self.description

        result: str = self.result

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "description": description,
                "result": result,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        description = d.pop("description")

        result = check_operation_business_condition_result_result(d.pop("result"))

        operation_business_condition_result = cls(
            id=id,
            description=description,
            result=result,
        )

        return operation_business_condition_result
