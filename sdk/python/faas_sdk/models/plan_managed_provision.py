from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.plan_managed_binding import PlanManagedBinding


T = TypeVar("T", bound="PlanManagedProvision")


@_attrs_define
class PlanManagedProvision:
    """The managed PostgreSQL database a project apply creates or reuses for a Compose postgres service, and the service
    keys it binds (ADR-20261025111824).

    """

    database: str
    bindings: list[PlanManagedBinding]
    existing: bool | Unset = UNSET
    """True when a database with this name already exists and is reused."""
    postgres_major: int | Unset = UNSET
    """The database's PostgreSQL major: the Compose image's, or the nearest newer one the platform offers."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        database = self.database

        bindings = []
        for bindings_item_data in self.bindings:
            bindings_item = bindings_item_data.to_dict()
            bindings.append(bindings_item)

        existing = self.existing

        postgres_major = self.postgres_major

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "database": database,
                "bindings": bindings,
            }
        )
        if existing is not UNSET:
            field_dict["existing"] = existing
        if postgres_major is not UNSET:
            field_dict["postgres_major"] = postgres_major

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.plan_managed_binding import PlanManagedBinding

        d = dict(src_dict)
        database = d.pop("database")

        bindings = []
        _bindings = d.pop("bindings")
        for bindings_item_data in _bindings:
            bindings_item = PlanManagedBinding.from_dict(bindings_item_data)

            bindings.append(bindings_item)

        existing = d.pop("existing", UNSET)

        postgres_major = d.pop("postgres_major", UNSET)

        plan_managed_provision = cls(
            database=database,
            bindings=bindings,
            existing=existing,
            postgres_major=postgres_major,
        )

        plan_managed_provision.additional_properties = d
        return plan_managed_provision

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
