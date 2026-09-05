from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operator_runtime_config_ack_status import (
    OperatorRuntimeConfigAckStatus,
    check_operator_runtime_config_ack_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperatorRuntimeConfigAck")


@_attrs_define
class OperatorRuntimeConfigAck:
    """One daemon/node acknowledgement of a runtime configuration version."""

    consumer: str
    version: int
    status: OperatorRuntimeConfigAckStatus
    updated_at: datetime.datetime
    node_id: str | Unset = UNSET
    effective_value: Any | Unset = UNSET
    error: str | Unset = UNSET
    applied_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        consumer = self.consumer
        version = self.version
        status: str = self.status
        updated_at = self.updated_at.isoformat()
        node_id = self.node_id
        effective_value = self.effective_value
        error = self.error
        applied_at: str | Unset = UNSET
        if not isinstance(self.applied_at, Unset):
            applied_at = self.applied_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({"consumer": consumer, "version": version, "status": status, "updated_at": updated_at})
        if node_id is not UNSET:
            field_dict["node_id"] = node_id
        if effective_value is not UNSET:
            field_dict["effective_value"] = effective_value
        if error is not UNSET:
            field_dict["error"] = error
        if applied_at is not UNSET:
            field_dict["applied_at"] = applied_at
        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        consumer = d.pop("consumer")
        version = d.pop("version")
        status = check_operator_runtime_config_ack_status(d.pop("status"))
        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))
        node_id = d.pop("node_id", UNSET)
        effective_value = d.pop("effective_value", UNSET)
        error = d.pop("error", UNSET)
        _applied_at = d.pop("applied_at", UNSET)
        applied_at: datetime.datetime | Unset
        if isinstance(_applied_at, Unset):
            applied_at = UNSET
        else:
            applied_at = datetime.datetime.fromisoformat(_applied_at)
        result = cls(
            consumer=consumer,
            version=version,
            status=status,
            updated_at=updated_at,
            node_id=node_id,
            effective_value=effective_value,
            error=error,
            applied_at=applied_at,
        )
        result.additional_properties = d
        return result

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
