from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_postgres_activity_cause import ManagedPostgresActivityCause
    from ..models.managed_postgres_activity_compute_start import ManagedPostgresActivityComputeStart
    from ..models.managed_postgres_activity_hint import ManagedPostgresActivityHint


T = TypeVar("T", bound="ManagedPostgresActivity")


@_attrs_define
class ManagedPostgresActivity:
    """Why a database was awake in a period (ADR-20261125231222). Causes are best guesses from timing."""

    database_id: str
    database_name: str
    since: datetime.datetime
    """Start of the period after clamping to the 30-day retention."""
    until: datetime.datetime
    starts: int
    """Recorded compute starts in the period."""
    awake_seconds: int
    """Seconds the compute was up in the period; a start still running counts to until."""
    causes: list[ManagedPostgresActivityCause]
    compute_starts: list[ManagedPostgresActivityComputeStart]
    """The latest starts, newest first, at most 200."""
    hints: list[ManagedPostgresActivityHint]
    docs_url: str
    suspend_timeout_seconds: int | Unset = UNSET
    """The suspend timeout in force; omitted for an always-on database or when it could not be read."""
    truncated: bool | Unset = UNSET
    """True when the period had more starts than compute_starts lists; the totals and causes count them all."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        database_id = self.database_id

        database_name = self.database_name

        since = self.since.isoformat()

        until = self.until.isoformat()

        starts = self.starts

        awake_seconds = self.awake_seconds

        causes = []
        for causes_item_data in self.causes:
            causes_item = causes_item_data.to_dict()
            causes.append(causes_item)

        compute_starts = []
        for compute_starts_item_data in self.compute_starts:
            compute_starts_item = compute_starts_item_data.to_dict()
            compute_starts.append(compute_starts_item)

        hints = []
        for hints_item_data in self.hints:
            hints_item = hints_item_data.to_dict()
            hints.append(hints_item)

        docs_url = self.docs_url

        suspend_timeout_seconds = self.suspend_timeout_seconds

        truncated = self.truncated

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "database_id": database_id,
                "database_name": database_name,
                "since": since,
                "until": until,
                "starts": starts,
                "awake_seconds": awake_seconds,
                "causes": causes,
                "compute_starts": compute_starts,
                "hints": hints,
                "docs_url": docs_url,
            }
        )
        if suspend_timeout_seconds is not UNSET:
            field_dict["suspend_timeout_seconds"] = suspend_timeout_seconds
        if truncated is not UNSET:
            field_dict["truncated"] = truncated

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_activity_cause import ManagedPostgresActivityCause
        from ..models.managed_postgres_activity_compute_start import ManagedPostgresActivityComputeStart
        from ..models.managed_postgres_activity_hint import ManagedPostgresActivityHint

        d = dict(src_dict)
        database_id = d.pop("database_id")

        database_name = d.pop("database_name")

        since = datetime.datetime.fromisoformat(d.pop("since"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        starts = d.pop("starts")

        awake_seconds = d.pop("awake_seconds")

        causes = []
        _causes = d.pop("causes")
        for causes_item_data in _causes:
            causes_item = ManagedPostgresActivityCause.from_dict(causes_item_data)

            causes.append(causes_item)

        compute_starts = []
        _compute_starts = d.pop("compute_starts")
        for compute_starts_item_data in _compute_starts:
            compute_starts_item = ManagedPostgresActivityComputeStart.from_dict(compute_starts_item_data)

            compute_starts.append(compute_starts_item)

        hints = []
        _hints = d.pop("hints")
        for hints_item_data in _hints:
            hints_item = ManagedPostgresActivityHint.from_dict(hints_item_data)

            hints.append(hints_item)

        docs_url = d.pop("docs_url")

        suspend_timeout_seconds = d.pop("suspend_timeout_seconds", UNSET)

        truncated = d.pop("truncated", UNSET)

        managed_postgres_activity = cls(
            database_id=database_id,
            database_name=database_name,
            since=since,
            until=until,
            starts=starts,
            awake_seconds=awake_seconds,
            causes=causes,
            compute_starts=compute_starts,
            hints=hints,
            docs_url=docs_url,
            suspend_timeout_seconds=suspend_timeout_seconds,
            truncated=truncated,
        )

        managed_postgres_activity.additional_properties = d
        return managed_postgres_activity

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
