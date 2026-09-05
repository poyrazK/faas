from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operator_runtime_config_apply_mode import (
    OperatorRuntimeConfigApplyMode,
    check_operator_runtime_config_apply_mode,
)
from ..models.operator_runtime_config_kind import OperatorRuntimeConfigKind, check_operator_runtime_config_kind
from ..models.operator_runtime_config_source import OperatorRuntimeConfigSource, check_operator_runtime_config_source
from ..models.operator_runtime_config_status import OperatorRuntimeConfigStatus, check_operator_runtime_config_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operator_runtime_config_ack import OperatorRuntimeConfigAck

T = TypeVar("T", bound="OperatorRuntimeConfig")


@_attrs_define
class OperatorRuntimeConfig:
    """One entry from the closed operator runtime-configuration catalog."""

    key: str
    label: str
    description: str
    category: str
    kind: OperatorRuntimeConfigKind
    default_value: Any
    desired_value: Any
    effective_value: Any
    source: OperatorRuntimeConfigSource
    apply_mode: OperatorRuntimeConfigApplyMode
    controller_enabled: bool
    mutable: bool
    sensitive: bool
    status: OperatorRuntimeConfigStatus
    version: int
    last_error: str | Unset = UNSET
    updated_at: datetime.datetime | Unset = UNSET
    applied_at: datetime.datetime | Unset = UNSET
    acks: list[OperatorRuntimeConfigAck] | Unset = UNSET
    """Per-daemon observations of the requested configuration version."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        label = self.label

        description = self.description

        category = self.category

        kind: str = self.kind

        default_value = self.default_value

        desired_value = self.desired_value

        effective_value = self.effective_value

        source: str = self.source

        apply_mode: str = self.apply_mode

        controller_enabled = self.controller_enabled

        mutable = self.mutable

        sensitive = self.sensitive

        status: str = self.status

        version = self.version

        last_error = self.last_error

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        applied_at: str | Unset = UNSET
        if not isinstance(self.applied_at, Unset):
            applied_at = self.applied_at.isoformat()

        acks: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.acks, Unset):
            acks = []
            for acks_item_data in self.acks:
                acks_item = acks_item_data.to_dict()
                acks.append(acks_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
                "label": label,
                "description": description,
                "category": category,
                "kind": kind,
                "default_value": default_value,
                "desired_value": desired_value,
                "effective_value": effective_value,
                "source": source,
                "apply_mode": apply_mode,
                "controller_enabled": controller_enabled,
                "mutable": mutable,
                "sensitive": sensitive,
                "status": status,
                "version": version,
            }
        )
        if last_error is not UNSET:
            field_dict["last_error"] = last_error
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at
        if applied_at is not UNSET:
            field_dict["applied_at"] = applied_at
        if acks is not UNSET:
            field_dict["acks"] = acks

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operator_runtime_config_ack import OperatorRuntimeConfigAck

        d = dict(src_dict)
        key = d.pop("key")

        label = d.pop("label")

        description = d.pop("description")

        category = d.pop("category")

        kind = check_operator_runtime_config_kind(d.pop("kind"))

        default_value = d.pop("default_value")

        desired_value = d.pop("desired_value")

        effective_value = d.pop("effective_value")

        source = check_operator_runtime_config_source(d.pop("source"))

        apply_mode = check_operator_runtime_config_apply_mode(d.pop("apply_mode"))

        controller_enabled = d.pop("controller_enabled")

        mutable = d.pop("mutable")

        sensitive = d.pop("sensitive")

        status = check_operator_runtime_config_status(d.pop("status"))

        version = d.pop("version")

        last_error = d.pop("last_error", UNSET)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        _applied_at = d.pop("applied_at", UNSET)
        applied_at: datetime.datetime | Unset
        if isinstance(_applied_at, Unset):
            applied_at = UNSET
        else:
            applied_at = datetime.datetime.fromisoformat(_applied_at)

        _acks = d.pop("acks", UNSET)
        acks: list[OperatorRuntimeConfigAck] | Unset = UNSET
        if _acks is not UNSET:
            acks = []
            for acks_item_data in _acks:
                acks_item = OperatorRuntimeConfigAck.from_dict(acks_item_data)

                acks.append(acks_item)

        operator_runtime_config = cls(
            key=key,
            label=label,
            description=description,
            category=category,
            kind=kind,
            default_value=default_value,
            desired_value=desired_value,
            effective_value=effective_value,
            source=source,
            apply_mode=apply_mode,
            controller_enabled=controller_enabled,
            mutable=mutable,
            sensitive=sensitive,
            status=status,
            version=version,
            last_error=last_error,
            updated_at=updated_at,
            applied_at=applied_at,
            acks=acks,
        )

        operator_runtime_config.additional_properties = d
        return operator_runtime_config

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
