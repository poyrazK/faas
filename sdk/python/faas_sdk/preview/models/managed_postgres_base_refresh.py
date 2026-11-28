from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_base_refresh_outcome import (
    ManagedPostgresBaseRefreshOutcome,
    check_managed_postgres_base_refresh_outcome,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresBaseRefresh")


@_attrs_define
class ManagedPostgresBaseRefresh:
    """Progress and outcome of replacing the database VM base image."""

    started_at: datetime.datetime
    outcome: ManagedPostgresBaseRefreshOutcome
    finished_at: datetime.datetime | Unset = UNSET
    phase: str | Unset = UNSET
    """Rolling refresh phase when applicable."""
    reason: str | Unset = UNSET
    """Reason the refresh is waiting or did not succeed."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        started_at = self.started_at.isoformat()

        outcome: str = self.outcome

        finished_at: str | Unset = UNSET
        if not isinstance(self.finished_at, Unset):
            finished_at = self.finished_at.isoformat()

        phase = self.phase

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "started_at": started_at,
                "outcome": outcome,
            }
        )
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at
        if phase is not UNSET:
            field_dict["phase"] = phase
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        outcome = check_managed_postgres_base_refresh_outcome(d.pop("outcome"))

        _finished_at = d.pop("finished_at", UNSET)
        finished_at: datetime.datetime | Unset
        if isinstance(_finished_at, Unset):
            finished_at = UNSET
        else:
            finished_at = datetime.datetime.fromisoformat(_finished_at)

        phase = d.pop("phase", UNSET)

        reason = d.pop("reason", UNSET)

        managed_postgres_base_refresh = cls(
            started_at=started_at,
            outcome=outcome,
            finished_at=finished_at,
            phase=phase,
            reason=reason,
        )

        managed_postgres_base_refresh.additional_properties = d
        return managed_postgres_base_refresh

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
