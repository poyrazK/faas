from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.dispatch_invocation_batch_response_200_results_item_status import (
    DispatchInvocationBatchResponse200ResultsItemStatus,
    check_dispatch_invocation_batch_response_200_results_item_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="DispatchInvocationBatchResponse200ResultsItem")


@_attrs_define
class DispatchInvocationBatchResponse200ResultsItem:
    item_identifier: str
    status: DispatchInvocationBatchResponse200ResultsItemStatus
    error: str | Unset = UNSET
    code: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        item_identifier = self.item_identifier

        status: str = self.status

        error = self.error

        code = self.code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "item_identifier": item_identifier,
                "status": status,
            }
        )
        if error is not UNSET:
            field_dict["error"] = error
        if code is not UNSET:
            field_dict["code"] = code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        item_identifier = d.pop("item_identifier")

        status = check_dispatch_invocation_batch_response_200_results_item_status(d.pop("status"))

        error = d.pop("error", UNSET)

        code = d.pop("code", UNSET)

        dispatch_invocation_batch_response_200_results_item = cls(
            item_identifier=item_identifier,
            status=status,
            error=error,
            code=code,
        )

        dispatch_invocation_batch_response_200_results_item.additional_properties = d
        return dispatch_invocation_batch_response_200_results_item

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
