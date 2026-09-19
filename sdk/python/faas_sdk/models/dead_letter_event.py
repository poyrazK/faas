from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.dead_letter_event_source import DeadLetterEventSource, check_dead_letter_event_source
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.dead_letter_event_error_detail import DeadLetterEventErrorDetail
    from ..models.dead_letter_event_headers import DeadLetterEventHeaders
    from ..models.dead_letter_event_payload import DeadLetterEventPayload


T = TypeVar("T", bound="DeadLetterEvent")


@_attrs_define
class DeadLetterEvent:
    """One unified queue invocation, broker trigger, or outbound webhook delivery dead-letter event."""

    id: str
    source: DeadLetterEventSource
    source_id: str
    payload: DeadLetterEventPayload
    headers: DeadLetterEventHeaders
    error_kind: str
    error_detail: DeadLetterEventErrorDetail
    retry_count: int
    first_failed_at: datetime.datetime
    last_failed_at: datetime.datetime
    created_at: datetime.datetime
    origin: str | Unset = UNSET
    """Invocation source or trigger kind."""
    trigger_id: str | Unset = UNSET
    replayed_at: datetime.datetime | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        source: str = self.source

        source_id = self.source_id

        payload = self.payload.to_dict()

        headers = self.headers.to_dict()

        error_kind = self.error_kind

        error_detail = self.error_detail.to_dict()

        retry_count = self.retry_count

        first_failed_at = self.first_failed_at.isoformat()

        last_failed_at = self.last_failed_at.isoformat()

        created_at = self.created_at.isoformat()

        origin = self.origin

        trigger_id = self.trigger_id

        replayed_at: None | str | Unset
        if isinstance(self.replayed_at, Unset):
            replayed_at = UNSET
        elif isinstance(self.replayed_at, datetime.datetime):
            replayed_at = self.replayed_at.isoformat()
        else:
            replayed_at = self.replayed_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "source": source,
                "source_id": source_id,
                "payload": payload,
                "headers": headers,
                "error_kind": error_kind,
                "error_detail": error_detail,
                "retry_count": retry_count,
                "first_failed_at": first_failed_at,
                "last_failed_at": last_failed_at,
                "created_at": created_at,
            }
        )
        if origin is not UNSET:
            field_dict["origin"] = origin
        if trigger_id is not UNSET:
            field_dict["trigger_id"] = trigger_id
        if replayed_at is not UNSET:
            field_dict["replayed_at"] = replayed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.dead_letter_event_error_detail import DeadLetterEventErrorDetail
        from ..models.dead_letter_event_headers import DeadLetterEventHeaders
        from ..models.dead_letter_event_payload import DeadLetterEventPayload

        d = dict(src_dict)
        id = d.pop("id")

        source = check_dead_letter_event_source(d.pop("source"))

        source_id = d.pop("source_id")

        payload = DeadLetterEventPayload.from_dict(d.pop("payload"))

        headers = DeadLetterEventHeaders.from_dict(d.pop("headers"))

        error_kind = d.pop("error_kind")

        error_detail = DeadLetterEventErrorDetail.from_dict(d.pop("error_detail"))

        retry_count = d.pop("retry_count")

        first_failed_at = datetime.datetime.fromisoformat(d.pop("first_failed_at"))

        last_failed_at = datetime.datetime.fromisoformat(d.pop("last_failed_at"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        origin = d.pop("origin", UNSET)

        trigger_id = d.pop("trigger_id", UNSET)

        def _parse_replayed_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                replayed_at_type_0 = datetime.datetime.fromisoformat(data)

                return replayed_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        replayed_at = _parse_replayed_at(d.pop("replayed_at", UNSET))

        dead_letter_event = cls(
            id=id,
            source=source,
            source_id=source_id,
            payload=payload,
            headers=headers,
            error_kind=error_kind,
            error_detail=error_detail,
            retry_count=retry_count,
            first_failed_at=first_failed_at,
            last_failed_at=last_failed_at,
            created_at=created_at,
            origin=origin,
            trigger_id=trigger_id,
            replayed_at=replayed_at,
        )

        dead_letter_event.additional_properties = d
        return dead_letter_event

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
