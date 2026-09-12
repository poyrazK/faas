from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.execution_response import ExecutionResponse


T = TypeVar("T", bound="ExecutionListResponse")


@_attrs_define
class ExecutionListResponse:
    """Account-scoped page of disposable execution receipts. `next_offset`
    is -1 when there is no following page; otherwise pass it as `offset`.

    """

    executions: list[ExecutionResponse]
    limit: int
    offset: int
    next_offset: int
    """Next offset, or -1 at the end of the result set."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        executions = []
        for executions_item_data in self.executions:
            executions_item = executions_item_data.to_dict()
            executions.append(executions_item)

        limit = self.limit

        offset = self.offset

        next_offset = self.next_offset

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "executions": executions,
                "limit": limit,
                "offset": offset,
                "next_offset": next_offset,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.execution_response import ExecutionResponse

        d = dict(src_dict)
        executions = []
        _executions = d.pop("executions")
        for executions_item_data in _executions:
            executions_item = ExecutionResponse.from_dict(executions_item_data)

            executions.append(executions_item)

        limit = d.pop("limit")

        offset = d.pop("offset")

        next_offset = d.pop("next_offset")

        execution_list_response = cls(
            executions=executions,
            limit=limit,
            offset=offset,
            next_offset=next_offset,
        )

        execution_list_response.additional_properties = d
        return execution_list_response

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
