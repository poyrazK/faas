from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresSession")


@_attrs_define
class ManagedPostgresSession:
    pid: int
    state: str
    transaction_age_ms: int
    wait_event_type: str | Unset = UNSET
    wait_event: str | Unset = UNSET
    application_name: str | Unset = UNSET
    target_id: UUID | Unset = UNSET
    """Short-lived cancel target for this execution when available."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        pid = self.pid

        state = self.state

        transaction_age_ms = self.transaction_age_ms

        wait_event_type = self.wait_event_type

        wait_event = self.wait_event

        application_name = self.application_name

        target_id: str | Unset = UNSET
        if not isinstance(self.target_id, Unset):
            target_id = str(self.target_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "pid": pid,
                "state": state,
                "transaction_age_ms": transaction_age_ms,
            }
        )
        if wait_event_type is not UNSET:
            field_dict["wait_event_type"] = wait_event_type
        if wait_event is not UNSET:
            field_dict["wait_event"] = wait_event
        if application_name is not UNSET:
            field_dict["application_name"] = application_name
        if target_id is not UNSET:
            field_dict["target_id"] = target_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        pid = d.pop("pid")

        state = d.pop("state")

        transaction_age_ms = d.pop("transaction_age_ms")

        wait_event_type = d.pop("wait_event_type", UNSET)

        wait_event = d.pop("wait_event", UNSET)

        application_name = d.pop("application_name", UNSET)

        _target_id = d.pop("target_id", UNSET)
        target_id: UUID | Unset
        if isinstance(_target_id, Unset):
            target_id = UNSET
        else:
            target_id = UUID(_target_id)

        managed_postgres_session = cls(
            pid=pid,
            state=state,
            transaction_age_ms=transaction_age_ms,
            wait_event_type=wait_event_type,
            wait_event=wait_event,
            application_name=application_name,
            target_id=target_id,
        )

        managed_postgres_session.additional_properties = d
        return managed_postgres_session

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
