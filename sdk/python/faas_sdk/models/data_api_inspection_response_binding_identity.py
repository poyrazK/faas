from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.data_api_inspection_response_binding_identity_status import (
    DataAPIInspectionResponseBindingIdentityStatus,
    check_data_api_inspection_response_binding_identity_status,
)

T = TypeVar("T", bound="DataAPIInspectionResponseBindingIdentity")


@_attrs_define
class DataAPIInspectionResponseBindingIdentity:
    status: DataAPIInspectionResponseBindingIdentityStatus
    reason: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "reason": reason,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_data_api_inspection_response_binding_identity_status(d.pop("status"))

        reason = d.pop("reason")

        data_api_inspection_response_binding_identity = cls(
            status=status,
            reason=reason,
        )

        data_api_inspection_response_binding_identity.additional_properties = d
        return data_api_inspection_response_binding_identity

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
