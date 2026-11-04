from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.prewarm_intent_response_status import PrewarmIntentResponseStatus, check_prewarm_intent_response_status
from ..models.prewarm_intent_response_trigger import PrewarmIntentResponseTrigger, check_prewarm_intent_response_trigger
from ..types import UNSET, Unset

T = TypeVar("T", bound="PrewarmIntentResponse")


@_attrs_define
class PrewarmIntentResponse:
    """Durable scheduled/predicted prewarm intent and scheduler outcome."""

    id: UUID
    app_id: UUID
    count: int
    wake_at: datetime.datetime
    expires_at: datetime.datetime
    trigger: PrewarmIntentResponseTrigger
    status: PrewarmIntentResponseStatus
    created_at: datetime.datetime
    claimed_at: datetime.datetime | None | Unset = UNSET
    fired_at: datetime.datetime | None | Unset = UNSET
    admitted_count: int | Unset = UNSET
    """Instances actually admitted; may be lower than count when capacity gates apply."""
    outcome: str | Unset = UNSET
    last_error: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        count = self.count

        wake_at = self.wake_at.isoformat()

        expires_at = self.expires_at.isoformat()

        trigger: str = self.trigger

        status: str = self.status

        created_at = self.created_at.isoformat()

        claimed_at: None | str | Unset
        if isinstance(self.claimed_at, Unset):
            claimed_at = UNSET
        elif isinstance(self.claimed_at, datetime.datetime):
            claimed_at = self.claimed_at.isoformat()
        else:
            claimed_at = self.claimed_at

        fired_at: None | str | Unset
        if isinstance(self.fired_at, Unset):
            fired_at = UNSET
        elif isinstance(self.fired_at, datetime.datetime):
            fired_at = self.fired_at.isoformat()
        else:
            fired_at = self.fired_at

        admitted_count = self.admitted_count

        outcome = self.outcome

        last_error = self.last_error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "count": count,
                "wake_at": wake_at,
                "expires_at": expires_at,
                "trigger": trigger,
                "status": status,
                "created_at": created_at,
            }
        )
        if claimed_at is not UNSET:
            field_dict["claimed_at"] = claimed_at
        if fired_at is not UNSET:
            field_dict["fired_at"] = fired_at
        if admitted_count is not UNSET:
            field_dict["admitted_count"] = admitted_count
        if outcome is not UNSET:
            field_dict["outcome"] = outcome
        if last_error is not UNSET:
            field_dict["last_error"] = last_error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        count = d.pop("count")

        wake_at = datetime.datetime.fromisoformat(d.pop("wake_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        trigger = check_prewarm_intent_response_trigger(d.pop("trigger"))

        status = check_prewarm_intent_response_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        def _parse_claimed_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                claimed_at_type_0 = datetime.datetime.fromisoformat(data)

                return claimed_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        claimed_at = _parse_claimed_at(d.pop("claimed_at", UNSET))

        def _parse_fired_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                fired_at_type_0 = datetime.datetime.fromisoformat(data)

                return fired_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        fired_at = _parse_fired_at(d.pop("fired_at", UNSET))

        admitted_count = d.pop("admitted_count", UNSET)

        outcome = d.pop("outcome", UNSET)

        last_error = d.pop("last_error", UNSET)

        prewarm_intent_response = cls(
            id=id,
            app_id=app_id,
            count=count,
            wake_at=wake_at,
            expires_at=expires_at,
            trigger=trigger,
            status=status,
            created_at=created_at,
            claimed_at=claimed_at,
            fired_at=fired_at,
            admitted_count=admitted_count,
            outcome=outcome,
            last_error=last_error,
        )

        prewarm_intent_response.additional_properties = d
        return prewarm_intent_response

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
