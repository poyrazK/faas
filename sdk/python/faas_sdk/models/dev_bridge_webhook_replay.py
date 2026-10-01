from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.dev_bridge_webhook_replay_state import DevBridgeWebhookReplayState, check_dev_bridge_webhook_replay_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="DevBridgeWebhookReplay")


@_attrs_define
class DevBridgeWebhookReplay:
    """Durable outcome of one development copy, separate from original delivery."""

    id: UUID
    session_id: str
    invocation_id: UUID
    state: DevBridgeWebhookReplayState
    http_status: int
    created_at: datetime.datetime
    completed_at: datetime.datetime | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        session_id = self.session_id

        invocation_id = str(self.invocation_id)

        state: str = self.state

        http_status = self.http_status

        created_at = self.created_at.isoformat()

        completed_at: None | str | Unset
        if isinstance(self.completed_at, Unset):
            completed_at = UNSET
        elif isinstance(self.completed_at, datetime.datetime):
            completed_at = self.completed_at.isoformat()
        else:
            completed_at = self.completed_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "session_id": session_id,
                "invocation_id": invocation_id,
                "state": state,
                "http_status": http_status,
                "created_at": created_at,
            }
        )
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        session_id = d.pop("session_id")

        invocation_id = UUID(d.pop("invocation_id"))

        state = check_dev_bridge_webhook_replay_state(d.pop("state"))

        http_status = d.pop("http_status")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        def _parse_completed_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                completed_at_type_0 = datetime.datetime.fromisoformat(data)

                return completed_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        completed_at = _parse_completed_at(d.pop("completed_at", UNSET))

        dev_bridge_webhook_replay = cls(
            id=id,
            session_id=session_id,
            invocation_id=invocation_id,
            state=state,
            http_status=http_status,
            created_at=created_at,
            completed_at=completed_at,
        )

        dev_bridge_webhook_replay.additional_properties = d
        return dev_bridge_webhook_replay

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
