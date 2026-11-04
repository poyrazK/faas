from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.data_api_connection_budgets_state import (
    DataAPIConnectionBudgetsState,
    check_data_api_connection_budgets_state,
)
from ..models.data_api_connection_budgets_version import (
    DataAPIConnectionBudgetsVersion,
    check_data_api_connection_budgets_version,
)

if TYPE_CHECKING:
    from ..models.managed_postgres_connection_budget import ManagedPostgresConnectionBudget


T = TypeVar("T", bound="DataAPIConnectionBudgets")


@_attrs_define
class DataAPIConnectionBudgets:
    """Read-only planning snapshots. Not applicable means the app has no visible Data API binding."""

    version: DataAPIConnectionBudgetsVersion
    app: str
    state: DataAPIConnectionBudgetsState
    truncated: bool
    databases: list[ManagedPostgresConnectionBudget]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        app = self.app

        state: str = self.state

        truncated = self.truncated

        databases = []
        for databases_item_data in self.databases:
            databases_item = databases_item_data.to_dict()
            databases.append(databases_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "app": app,
                "state": state,
                "truncated": truncated,
                "databases": databases,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_connection_budget import ManagedPostgresConnectionBudget

        d = dict(src_dict)
        version = check_data_api_connection_budgets_version(d.pop("version"))

        app = d.pop("app")

        state = check_data_api_connection_budgets_state(d.pop("state"))

        truncated = d.pop("truncated")

        databases = []
        _databases = d.pop("databases")
        for databases_item_data in _databases:
            databases_item = ManagedPostgresConnectionBudget.from_dict(databases_item_data)

            databases.append(databases_item)

        data_api_connection_budgets = cls(
            version=version,
            app=app,
            state=state,
            truncated=truncated,
            databases=databases,
        )

        data_api_connection_budgets.additional_properties = d
        return data_api_connection_budgets

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
