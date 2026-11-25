from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_sessions_state import ManagedPostgresSessionsState, check_managed_postgres_sessions_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_postgres_session_sample import ManagedPostgresSessionSample


T = TypeVar("T", bound="ManagedPostgresSessions")


@_attrs_define
class ManagedPostgresSessions:
    """Current session sample availability and tracked pooled queries."""

    database_id: UUID
    state: ManagedPostgresSessionsState
    sample: ManagedPostgresSessionSample | Unset = UNSET
    """Timestamped snapshot of sessions observed on the running primary."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        database_id = str(self.database_id)

        state: str = self.state

        sample: dict[str, Any] | Unset = UNSET
        if not isinstance(self.sample, Unset):
            sample = self.sample.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "database_id": database_id,
                "state": state,
            }
        )
        if sample is not UNSET:
            field_dict["sample"] = sample

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_session_sample import ManagedPostgresSessionSample

        d = dict(src_dict)
        database_id = UUID(d.pop("database_id"))

        state = check_managed_postgres_sessions_state(d.pop("state"))

        _sample = d.pop("sample", UNSET)
        sample: ManagedPostgresSessionSample | Unset
        if isinstance(_sample, Unset):
            sample = UNSET
        else:
            sample = ManagedPostgresSessionSample.from_dict(_sample)

        managed_postgres_sessions = cls(
            database_id=database_id,
            state=state,
            sample=sample,
        )

        managed_postgres_sessions.additional_properties = d
        return managed_postgres_sessions

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
