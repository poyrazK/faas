from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_operation_check_status import AppOperationCheckStatus, check_app_operation_check_status

T = TypeVar("T", bound="AppOperationCheck")


@_attrs_define
class AppOperationCheck:
    """One current native policy observation with a stable reason code."""

    status: AppOperationCheckStatus
    reason: str
    """Stable native policy reason code; no customer-provided text."""
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
        status = check_app_operation_check_status(d.pop("status"))

        reason = d.pop("reason")

        app_operation_check = cls(
            status=status,
            reason=reason,
        )

        app_operation_check.additional_properties = d
        return app_operation_check

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
