from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_connection_budget_model import (
    ManagedPostgresConnectionBudgetModel,
    check_managed_postgres_connection_budget_model,
)
from ..models.managed_postgres_connection_budget_state import (
    ManagedPostgresConnectionBudgetState,
    check_managed_postgres_connection_budget_state,
)
from ..models.managed_postgres_connection_budget_version import (
    ManagedPostgresConnectionBudgetVersion,
    check_managed_postgres_connection_budget_version,
)

if TYPE_CHECKING:
    from ..models.connection_budget_capacity import ConnectionBudgetCapacity
    from ..models.connection_budget_demand import ConnectionBudgetDemand
    from ..models.connection_budget_inventory import ConnectionBudgetInventory


T = TypeVar("T", bound="ManagedPostgresConnectionBudget")


@_attrs_define
class ManagedPostgresConnectionBudget:
    """Capacity evidence and demand. Canonical pool planning remains unknown without complete evidence. Qualified mirrors
    enforce shared reservations and login ceilings; their modeled peak includes all reservations sharing physical
    limits. Runtime pool estimate fields are zero in that model.

    """

    version: ManagedPostgresConnectionBudgetVersion
    database_id: UUID
    database_name: str
    state: ManagedPostgresConnectionBudgetState
    checked_at: datetime.datetime
    expires_at: datetime.datetime
    enforced: bool
    model: ManagedPostgresConnectionBudgetModel
    inventory: ConnectionBudgetInventory
    """Deduplicated app/scope potential consumers; counts are not live SQL sessions."""
    demand: ConnectionBudgetDemand
    """Canonical runtime pools and explicit planning headroom. Unbounded consumers remain unknown."""
    capacity: ConnectionBudgetCapacity
    """Qualified tenant-usable allocation for the exact database generation and placement. Current adapters leave
    this unknown."""
    reasons: list[str]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        database_id = str(self.database_id)

        database_name = self.database_name

        state: str = self.state

        checked_at = self.checked_at.isoformat()

        expires_at = self.expires_at.isoformat()

        enforced = self.enforced

        model: str = self.model

        inventory = self.inventory.to_dict()

        demand = self.demand.to_dict()

        capacity = self.capacity.to_dict()

        reasons = self.reasons

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "database_id": database_id,
                "database_name": database_name,
                "state": state,
                "checked_at": checked_at,
                "expires_at": expires_at,
                "enforced": enforced,
                "model": model,
                "inventory": inventory,
                "demand": demand,
                "capacity": capacity,
                "reasons": reasons,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.connection_budget_capacity import ConnectionBudgetCapacity
        from ..models.connection_budget_demand import ConnectionBudgetDemand
        from ..models.connection_budget_inventory import ConnectionBudgetInventory

        d = dict(src_dict)
        version = check_managed_postgres_connection_budget_version(d.pop("version"))

        database_id = UUID(d.pop("database_id"))

        database_name = d.pop("database_name")

        state = check_managed_postgres_connection_budget_state(d.pop("state"))

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        enforced = d.pop("enforced")

        model = check_managed_postgres_connection_budget_model(d.pop("model"))

        inventory = ConnectionBudgetInventory.from_dict(d.pop("inventory"))

        demand = ConnectionBudgetDemand.from_dict(d.pop("demand"))

        capacity = ConnectionBudgetCapacity.from_dict(d.pop("capacity"))

        reasons = cast(list[str], d.pop("reasons"))

        managed_postgres_connection_budget = cls(
            version=version,
            database_id=database_id,
            database_name=database_name,
            state=state,
            checked_at=checked_at,
            expires_at=expires_at,
            enforced=enforced,
            model=model,
            inventory=inventory,
            demand=demand,
            capacity=capacity,
            reasons=reasons,
        )

        managed_postgres_connection_budget.additional_properties = d
        return managed_postgres_connection_budget

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
