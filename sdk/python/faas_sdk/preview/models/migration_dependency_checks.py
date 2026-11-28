from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.migration_service_connection_check import MigrationServiceConnectionCheck


T = TypeVar("T", bound="MigrationDependencyChecks")


@_attrs_define
class MigrationDependencyChecks:
    """Existing scoped environment names; values are resolved only in the task guest."""

    postgres_env_key: str | Unset = UNSET
    """PostgreSQL connection string environment key for authenticated SELECT 1."""
    object_storage_prefix: str | Unset = UNSET
    """Prefix for the existing six S3 binding variables; read-only listing."""
    http_env_key: str | Unset = UNSET
    """Environment key containing an HTTPS external API URL for a bounded GET; redirects are rejected."""
    service_connections: list[MigrationServiceConnectionCheck] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        postgres_env_key = self.postgres_env_key

        object_storage_prefix = self.object_storage_prefix

        http_env_key = self.http_env_key

        service_connections: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.service_connections, Unset):
            service_connections = []
            for service_connections_item_data in self.service_connections:
                service_connections_item = service_connections_item_data.to_dict()
                service_connections.append(service_connections_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if postgres_env_key is not UNSET:
            field_dict["postgres_env_key"] = postgres_env_key
        if object_storage_prefix is not UNSET:
            field_dict["object_storage_prefix"] = object_storage_prefix
        if http_env_key is not UNSET:
            field_dict["http_env_key"] = http_env_key
        if service_connections is not UNSET:
            field_dict["service_connections"] = service_connections

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.migration_service_connection_check import MigrationServiceConnectionCheck

        d = dict(src_dict)
        postgres_env_key = d.pop("postgres_env_key", UNSET)

        object_storage_prefix = d.pop("object_storage_prefix", UNSET)

        http_env_key = d.pop("http_env_key", UNSET)

        _service_connections = d.pop("service_connections", UNSET)
        service_connections: list[MigrationServiceConnectionCheck] | Unset = UNSET
        if _service_connections is not UNSET:
            service_connections = []
            for service_connections_item_data in _service_connections:
                service_connections_item = MigrationServiceConnectionCheck.from_dict(service_connections_item_data)

                service_connections.append(service_connections_item)

        migration_dependency_checks = cls(
            postgres_env_key=postgres_env_key,
            object_storage_prefix=object_storage_prefix,
            http_env_key=http_env_key,
            service_connections=service_connections,
        )

        migration_dependency_checks.additional_properties = d
        return migration_dependency_checks

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
