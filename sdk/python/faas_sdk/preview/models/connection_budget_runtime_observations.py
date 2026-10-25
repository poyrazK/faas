from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.connection_budget_runtime_observations_source import (
    ConnectionBudgetRuntimeObservationsSource,
    check_connection_budget_runtime_observations_source,
)
from ..models.connection_budget_runtime_observations_state import (
    ConnectionBudgetRuntimeObservationsState,
    check_connection_budget_runtime_observations_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ConnectionBudgetRuntimeObservations")


@_attrs_define
class ConnectionBudgetRuntimeObservations:
    """Authenticated workload reports of configured resident Data API pools, including the refresh reader. Not live SQL
    sessions or independent guest attestation. Parked runtimes and future wakes remain estimates; this does not verify
    Auth or external consumers.

    """

    state: ConnectionBudgetRuntimeObservationsState
    source: ConnectionBudgetRuntimeObservationsSource
    observed_instances: int
    unobserved_instances: int
    parked_instances: int
    configured_connections: int
    checked_at: datetime.datetime | Unset = UNSET
    expires_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        source: str = self.source

        observed_instances = self.observed_instances

        unobserved_instances = self.unobserved_instances

        parked_instances = self.parked_instances

        configured_connections = self.configured_connections

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "source": source,
                "observed_instances": observed_instances,
                "unobserved_instances": unobserved_instances,
                "parked_instances": parked_instances,
                "configured_connections": configured_connections,
            }
        )
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = check_connection_budget_runtime_observations_state(d.pop("state"))

        source = check_connection_budget_runtime_observations_source(d.pop("source"))

        observed_instances = d.pop("observed_instances")

        unobserved_instances = d.pop("unobserved_instances")

        parked_instances = d.pop("parked_instances")

        configured_connections = d.pop("configured_connections")

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        connection_budget_runtime_observations = cls(
            state=state,
            source=source,
            observed_instances=observed_instances,
            unobserved_instances=unobserved_instances,
            parked_instances=parked_instances,
            configured_connections=configured_connections,
            checked_at=checked_at,
            expires_at=expires_at,
        )

        connection_budget_runtime_observations.additional_properties = d
        return connection_budget_runtime_observations

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
