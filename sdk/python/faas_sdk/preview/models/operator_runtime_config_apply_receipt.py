from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operator_runtime_config_apply_receipt_status import (
    OperatorRuntimeConfigApplyReceiptStatus,
    check_operator_runtime_config_apply_receipt_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperatorRuntimeConfigApplyReceipt")


@_attrs_define
class OperatorRuntimeConfigApplyReceipt:
    """What one daemon instance reported applying for a runtime-configuration key."""

    config_key: str
    daemon: str
    instance: str
    version: int
    status: OperatorRuntimeConfigApplyReceiptStatus
    reported_at: datetime.datetime
    error: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        config_key = self.config_key

        daemon = self.daemon

        instance = self.instance

        version = self.version

        status: str = self.status

        reported_at = self.reported_at.isoformat()

        error = self.error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "config_key": config_key,
                "daemon": daemon,
                "instance": instance,
                "version": version,
                "status": status,
                "reported_at": reported_at,
            }
        )
        if error is not UNSET:
            field_dict["error"] = error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        config_key = d.pop("config_key")

        daemon = d.pop("daemon")

        instance = d.pop("instance")

        version = d.pop("version")

        status = check_operator_runtime_config_apply_receipt_status(d.pop("status"))

        reported_at = datetime.datetime.fromisoformat(d.pop("reported_at"))

        error = d.pop("error", UNSET)

        operator_runtime_config_apply_receipt = cls(
            config_key=config_key,
            daemon=daemon,
            instance=instance,
            version=version,
            status=status,
            reported_at=reported_at,
            error=error,
        )

        operator_runtime_config_apply_receipt.additional_properties = d
        return operator_runtime_config_apply_receipt

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
