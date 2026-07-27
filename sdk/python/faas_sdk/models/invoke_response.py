from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.invoke_response_status import InvokeResponseStatus, check_invoke_response_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.invoke_response_result import InvokeResponseResult


T = TypeVar("T", bound="InvokeResponse")


@_attrs_define
class InvokeResponse:
    """Sync-invoke result. Status is the drain-driven terminal state (`completed` | `failed` | `cancelled`). Result is the
    original row's payload cast to JSON (omitted while still pending).

    """

    id: str
    status: InvokeResponseStatus
    result: InvokeResponseResult | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        status: str = self.status

        result: dict[str, Any] | Unset = UNSET
        if not isinstance(self.result, Unset):
            result = self.result.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "status": status,
            }
        )
        if result is not UNSET:
            field_dict["result"] = result

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.invoke_response_result import InvokeResponseResult

        d = dict(src_dict)
        id = d.pop("id")

        status = check_invoke_response_status(d.pop("status"))

        _result = d.pop("result", UNSET)
        result: InvokeResponseResult | Unset
        if isinstance(_result, Unset):
            result = UNSET
        else:
            result = InvokeResponseResult.from_dict(_result)

        invoke_response = cls(
            id=id,
            status=status,
            result=result,
        )

        invoke_response.additional_properties = d
        return invoke_response

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
