from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.application_standard_settings_security_policy import (
    ApplicationStandardSettingsSecurityPolicy,
    check_application_standard_settings_security_policy,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ApplicationStandardSettings")


@_attrs_define
class ApplicationStandardSettings:
    """Logical control values; resource references contain UUIDs, never credentials."""

    log_destinations: list[UUID] | Unset = UNSET
    require_signed: bool | Unset = UNSET
    security_policy: ApplicationStandardSettingsSecurityPolicy | Unset = UNSET
    trusted_publishers: list[UUID] | Unset = UNSET
    egress_cidrs: list[str] | Unset = UNSET
    egress_extra_ports: list[int] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        log_destinations: list[str] | Unset = UNSET
        if not isinstance(self.log_destinations, Unset):
            log_destinations = []
            for log_destinations_item_data in self.log_destinations:
                log_destinations_item = str(log_destinations_item_data)
                log_destinations.append(log_destinations_item)

        require_signed = self.require_signed

        security_policy: str | Unset = UNSET
        if not isinstance(self.security_policy, Unset):
            security_policy = self.security_policy

        trusted_publishers: list[str] | Unset = UNSET
        if not isinstance(self.trusted_publishers, Unset):
            trusted_publishers = []
            for trusted_publishers_item_data in self.trusted_publishers:
                trusted_publishers_item = str(trusted_publishers_item_data)
                trusted_publishers.append(trusted_publishers_item)

        egress_cidrs: list[str] | Unset = UNSET
        if not isinstance(self.egress_cidrs, Unset):
            egress_cidrs = self.egress_cidrs

        egress_extra_ports: list[int] | Unset = UNSET
        if not isinstance(self.egress_extra_ports, Unset):
            egress_extra_ports = self.egress_extra_ports

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if log_destinations is not UNSET:
            field_dict["log_destinations"] = log_destinations
        if require_signed is not UNSET:
            field_dict["require_signed"] = require_signed
        if security_policy is not UNSET:
            field_dict["security_policy"] = security_policy
        if trusted_publishers is not UNSET:
            field_dict["trusted_publishers"] = trusted_publishers
        if egress_cidrs is not UNSET:
            field_dict["egress_cidrs"] = egress_cidrs
        if egress_extra_ports is not UNSET:
            field_dict["egress_extra_ports"] = egress_extra_ports

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _log_destinations = d.pop("log_destinations", UNSET)
        log_destinations: list[UUID] | Unset = UNSET
        if _log_destinations is not UNSET:
            log_destinations = []
            for log_destinations_item_data in _log_destinations:
                log_destinations_item = UUID(log_destinations_item_data)

                log_destinations.append(log_destinations_item)

        require_signed = d.pop("require_signed", UNSET)

        _security_policy = d.pop("security_policy", UNSET)
        security_policy: ApplicationStandardSettingsSecurityPolicy | Unset
        if isinstance(_security_policy, Unset):
            security_policy = UNSET
        else:
            security_policy = check_application_standard_settings_security_policy(_security_policy)

        _trusted_publishers = d.pop("trusted_publishers", UNSET)
        trusted_publishers: list[UUID] | Unset = UNSET
        if _trusted_publishers is not UNSET:
            trusted_publishers = []
            for trusted_publishers_item_data in _trusted_publishers:
                trusted_publishers_item = UUID(trusted_publishers_item_data)

                trusted_publishers.append(trusted_publishers_item)

        egress_cidrs = cast(list[str], d.pop("egress_cidrs", UNSET))

        egress_extra_ports = cast(list[int], d.pop("egress_extra_ports", UNSET))

        application_standard_settings = cls(
            log_destinations=log_destinations,
            require_signed=require_signed,
            security_policy=security_policy,
            trusted_publishers=trusted_publishers,
            egress_cidrs=egress_cidrs,
            egress_extra_ports=egress_extra_ports,
        )

        return application_standard_settings
