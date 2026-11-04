from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.migration_dependency_checks import MigrationDependencyChecks


T = TypeVar("T", bound="CreateMigrationVerificationRequest")


@_attrs_define
class CreateMigrationVerificationRequest:
    """Immutable migration checks and explicit consent to park and wake the selected Gregale app."""

    id: UUID
    """Stable caller-generated identity for creation recovery."""
    deployment_id: UUID
    checks: MigrationDependencyChecks
    """Existing scoped environment names; values are resolved only in the task guest."""
    smoke_path: str
    """Read-only GET path on this app; no query, fragment or absolute URL. A successful 2xx response is required."""
    cold_wake: bool
    """Explicitly park this Gregale app and verify a correlated wake followed by repeated checks."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        deployment_id = str(self.deployment_id)

        checks = self.checks.to_dict()

        smoke_path = self.smoke_path

        cold_wake = self.cold_wake

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "deployment_id": deployment_id,
                "checks": checks,
                "smoke_path": smoke_path,
                "cold_wake": cold_wake,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.migration_dependency_checks import MigrationDependencyChecks

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        deployment_id = UUID(d.pop("deployment_id"))

        checks = MigrationDependencyChecks.from_dict(d.pop("checks"))

        smoke_path = d.pop("smoke_path")

        cold_wake = d.pop("cold_wake")

        create_migration_verification_request = cls(
            id=id,
            deployment_id=deployment_id,
            checks=checks,
            smoke_path=smoke_path,
            cold_wake=cold_wake,
        )

        create_migration_verification_request.additional_properties = d
        return create_migration_verification_request

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
