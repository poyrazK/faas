from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_capabilities_availability_item import (
    ManagedPostgresCapabilitiesAvailabilityItem,
    check_managed_postgres_capabilities_availability_item,
)
from ..models.managed_postgres_capabilities_credential_access_item import (
    ManagedPostgresCapabilitiesCredentialAccessItem,
    check_managed_postgres_capabilities_credential_access_item,
)
from ..models.managed_postgres_capabilities_service_classes_item import (
    ManagedPostgresCapabilitiesServiceClassesItem,
    check_managed_postgres_capabilities_service_classes_item,
)

T = TypeVar("T", bound="ManagedPostgresCapabilities")


@_attrs_define
class ManagedPostgresCapabilities:
    """Versioned PostgreSQL support for the default backend in a region, intersected with customer plan limits. Provider
    identities, costs, endpoints and credentials are excluded. Capabilities remain visible while the rollout gate is
    closed.

    """

    contract_version: int
    region: str
    provisioning_enabled: bool
    database_limit: int
    postgres_majors: list[int]
    service_classes: list[ManagedPostgresCapabilitiesServiceClassesItem]
    availability: list[ManagedPostgresCapabilitiesAvailabilityItem]
    credential_access: list[ManagedPostgresCapabilitiesCredentialAccessItem]
    scale_to_zero: bool
    always_on: bool
    pooled_connections: bool
    point_in_time_restore: bool
    storage_limit_bytes: int
    restore_window_seconds: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        contract_version = self.contract_version

        region = self.region

        provisioning_enabled = self.provisioning_enabled

        database_limit = self.database_limit

        postgres_majors = self.postgres_majors

        service_classes = []
        for service_classes_item_data in self.service_classes:
            service_classes_item: str = service_classes_item_data
            service_classes.append(service_classes_item)

        availability = []
        for availability_item_data in self.availability:
            availability_item: str = availability_item_data
            availability.append(availability_item)

        credential_access = []
        for credential_access_item_data in self.credential_access:
            credential_access_item: str = credential_access_item_data
            credential_access.append(credential_access_item)

        scale_to_zero = self.scale_to_zero

        always_on = self.always_on

        pooled_connections = self.pooled_connections

        point_in_time_restore = self.point_in_time_restore

        storage_limit_bytes = self.storage_limit_bytes

        restore_window_seconds = self.restore_window_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "contract_version": contract_version,
                "region": region,
                "provisioning_enabled": provisioning_enabled,
                "database_limit": database_limit,
                "postgres_majors": postgres_majors,
                "service_classes": service_classes,
                "availability": availability,
                "credential_access": credential_access,
                "scale_to_zero": scale_to_zero,
                "always_on": always_on,
                "pooled_connections": pooled_connections,
                "point_in_time_restore": point_in_time_restore,
                "storage_limit_bytes": storage_limit_bytes,
                "restore_window_seconds": restore_window_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        contract_version = d.pop("contract_version")

        region = d.pop("region")

        provisioning_enabled = d.pop("provisioning_enabled")

        database_limit = d.pop("database_limit")

        postgres_majors = cast(list[int], d.pop("postgres_majors"))

        service_classes = []
        _service_classes = d.pop("service_classes")
        for service_classes_item_data in _service_classes:
            service_classes_item = check_managed_postgres_capabilities_service_classes_item(service_classes_item_data)

            service_classes.append(service_classes_item)

        availability = []
        _availability = d.pop("availability")
        for availability_item_data in _availability:
            availability_item = check_managed_postgres_capabilities_availability_item(availability_item_data)

            availability.append(availability_item)

        credential_access = []
        _credential_access = d.pop("credential_access")
        for credential_access_item_data in _credential_access:
            credential_access_item = check_managed_postgres_capabilities_credential_access_item(
                credential_access_item_data
            )

            credential_access.append(credential_access_item)

        scale_to_zero = d.pop("scale_to_zero")

        always_on = d.pop("always_on")

        pooled_connections = d.pop("pooled_connections")

        point_in_time_restore = d.pop("point_in_time_restore")

        storage_limit_bytes = d.pop("storage_limit_bytes")

        restore_window_seconds = d.pop("restore_window_seconds")

        managed_postgres_capabilities = cls(
            contract_version=contract_version,
            region=region,
            provisioning_enabled=provisioning_enabled,
            database_limit=database_limit,
            postgres_majors=postgres_majors,
            service_classes=service_classes,
            availability=availability,
            credential_access=credential_access,
            scale_to_zero=scale_to_zero,
            always_on=always_on,
            pooled_connections=pooled_connections,
            point_in_time_restore=point_in_time_restore,
            storage_limit_bytes=storage_limit_bytes,
            restore_window_seconds=restore_window_seconds,
        )

        managed_postgres_capabilities.additional_properties = d
        return managed_postgres_capabilities

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
