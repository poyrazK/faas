from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operator_platform_flag_attributes_item import (
    OperatorPlatformFlagAttributesItem,
    check_operator_platform_flag_attributes_item,
)
from ..models.operator_platform_flag_maturity import OperatorPlatformFlagMaturity, check_operator_platform_flag_maturity
from ..models.operator_platform_flag_source import OperatorPlatformFlagSource, check_operator_platform_flag_source
from ..models.operator_platform_flag_status import OperatorPlatformFlagStatus, check_operator_platform_flag_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operator_platform_flag_convergence import OperatorPlatformFlagConvergence
    from ..models.operator_platform_flag_opt_ins import OperatorPlatformFlagOptIns
    from ..models.operator_runtime_config_apply_receipt import OperatorRuntimeConfigApplyReceipt
    from ..models.platform_flag_definition import PlatformFlagDefinition


T = TypeVar("T", bound="OperatorPlatformFlag")


@_attrs_define
class OperatorPlatformFlag:
    """One catalogued platform flag with its live definition, apply receipts and convergence."""

    key: str
    config_key: str
    """Runtime-configuration key that stores the flag; write it with PATCH /v1/admin/config/{key}."""
    label: str
    description: str
    adr: str
    consumers: list[str]
    attributes: list[OperatorPlatformFlagAttributesItem]
    """Subject attributes every consumer can supply, so rules may constrain them; a write using any other is
    refused (ADR 20261021080640)."""
    legacy_env: list[str]
    definition: PlatformFlagDefinition
    """Stored value of a platform flag. Enabled is the kill switch; default applies to accounts no rule matches."""
    source: OperatorPlatformFlagSource
    version: int
    status: OperatorPlatformFlagStatus
    receipts: list[OperatorRuntimeConfigApplyReceipt]
    convergence: OperatorPlatformFlagConvergence
    """Consumers of a platform flag grouped by whether every instance applied its current version."""
    opt_ins: OperatorPlatformFlagOptIns
    """How many accounts and organizations joined a self-serve preview, and the opt-in row's version."""
    capability: str | Unset = UNSET
    retire_by: datetime.date | Unset = UNSET
    last_error: str | Unset = UNSET
    updated_at: datetime.datetime | Unset = UNSET
    maturity: OperatorPlatformFlagMaturity | Unset = UNSET
    """Tier of the capability the flag releases (ADR-20261023115741)."""
    parent: str | Unset = UNSET
    """Parent capability flag; a sub-feature is released only while its parent is."""
    core: bool | Unset = UNSET
    """Core capability flag: turning it off for every account needs force."""
    enforcement: str | Unset = UNSET
    """What the flag gates today, joined with +: routes, settings, daemons; or handler or none."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        config_key = self.config_key

        label = self.label

        description = self.description

        adr = self.adr

        consumers = self.consumers

        attributes = []
        for attributes_item_data in self.attributes:
            attributes_item: str = attributes_item_data
            attributes.append(attributes_item)

        legacy_env = self.legacy_env

        definition = self.definition.to_dict()

        source: str = self.source

        version = self.version

        status: str = self.status

        receipts = []
        for receipts_item_data in self.receipts:
            receipts_item = receipts_item_data.to_dict()
            receipts.append(receipts_item)

        convergence = self.convergence.to_dict()

        opt_ins = self.opt_ins.to_dict()

        capability = self.capability

        retire_by: str | Unset = UNSET
        if not isinstance(self.retire_by, Unset):
            retire_by = self.retire_by.isoformat()

        last_error = self.last_error

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        maturity: str | Unset = UNSET
        if not isinstance(self.maturity, Unset):
            maturity = self.maturity

        parent = self.parent

        core = self.core

        enforcement = self.enforcement

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
                "config_key": config_key,
                "label": label,
                "description": description,
                "adr": adr,
                "consumers": consumers,
                "attributes": attributes,
                "legacy_env": legacy_env,
                "definition": definition,
                "source": source,
                "version": version,
                "status": status,
                "receipts": receipts,
                "convergence": convergence,
                "opt_ins": opt_ins,
            }
        )
        if capability is not UNSET:
            field_dict["capability"] = capability
        if retire_by is not UNSET:
            field_dict["retire_by"] = retire_by
        if last_error is not UNSET:
            field_dict["last_error"] = last_error
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at
        if maturity is not UNSET:
            field_dict["maturity"] = maturity
        if parent is not UNSET:
            field_dict["parent"] = parent
        if core is not UNSET:
            field_dict["core"] = core
        if enforcement is not UNSET:
            field_dict["enforcement"] = enforcement

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operator_platform_flag_convergence import OperatorPlatformFlagConvergence
        from ..models.operator_platform_flag_opt_ins import OperatorPlatformFlagOptIns
        from ..models.operator_runtime_config_apply_receipt import OperatorRuntimeConfigApplyReceipt
        from ..models.platform_flag_definition import PlatformFlagDefinition

        d = dict(src_dict)
        key = d.pop("key")

        config_key = d.pop("config_key")

        label = d.pop("label")

        description = d.pop("description")

        adr = d.pop("adr")

        consumers = cast(list[str], d.pop("consumers"))

        attributes = []
        _attributes = d.pop("attributes")
        for attributes_item_data in _attributes:
            attributes_item = check_operator_platform_flag_attributes_item(attributes_item_data)

            attributes.append(attributes_item)

        legacy_env = cast(list[str], d.pop("legacy_env"))

        definition = PlatformFlagDefinition.from_dict(d.pop("definition"))

        source = check_operator_platform_flag_source(d.pop("source"))

        version = d.pop("version")

        status = check_operator_platform_flag_status(d.pop("status"))

        receipts = []
        _receipts = d.pop("receipts")
        for receipts_item_data in _receipts:
            receipts_item = OperatorRuntimeConfigApplyReceipt.from_dict(receipts_item_data)

            receipts.append(receipts_item)

        convergence = OperatorPlatformFlagConvergence.from_dict(d.pop("convergence"))

        opt_ins = OperatorPlatformFlagOptIns.from_dict(d.pop("opt_ins"))

        capability = d.pop("capability", UNSET)

        _retire_by = d.pop("retire_by", UNSET)
        retire_by: datetime.date | Unset
        if isinstance(_retire_by, Unset):
            retire_by = UNSET
        else:
            retire_by = datetime.date.fromisoformat(_retire_by)

        last_error = d.pop("last_error", UNSET)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        _maturity = d.pop("maturity", UNSET)
        maturity: OperatorPlatformFlagMaturity | Unset
        if isinstance(_maturity, Unset):
            maturity = UNSET
        else:
            maturity = check_operator_platform_flag_maturity(_maturity)

        parent = d.pop("parent", UNSET)

        core = d.pop("core", UNSET)

        enforcement = d.pop("enforcement", UNSET)

        operator_platform_flag = cls(
            key=key,
            config_key=config_key,
            label=label,
            description=description,
            adr=adr,
            consumers=consumers,
            attributes=attributes,
            legacy_env=legacy_env,
            definition=definition,
            source=source,
            version=version,
            status=status,
            receipts=receipts,
            convergence=convergence,
            opt_ins=opt_ins,
            capability=capability,
            retire_by=retire_by,
            last_error=last_error,
            updated_at=updated_at,
            maturity=maturity,
            parent=parent,
            core=core,
            enforcement=enforcement,
        )

        operator_platform_flag.additional_properties = d
        return operator_platform_flag

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
