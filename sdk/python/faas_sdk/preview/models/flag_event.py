from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.flag_event_flags import FlagEventFlags
    from ..models.flag_event_properties import FlagEventProperties


T = TypeVar("T", bound="FlagEvent")


@_attrs_define
class FlagEvent:
    """One conversion event a runtime SDK reports for experiments."""

    name: str
    """Conversion event name."""
    customer_id: UUID
    """Verified customer (platform tenant) of the request the event belongs to."""
    value: int | Unset = UNSET
    """Optional integer value, such as cents."""
    at: datetime.datetime | Unset = UNSET
    """When the event happened; within the last 24 hours. Defaults to now."""
    subject: str | Unset = UNSET
    """Optional end-user subject of the request; only its hash is stored."""
    properties: FlagEventProperties | Unset = UNSET
    """Optional string properties; keys match ^[a-z][a-z0-9_]{0,63}$, values are at most 256 bytes."""
    flags: FlagEventFlags | Unset = UNSET
    """Served value, as a string, of each flag the request evaluated before the event."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        customer_id = str(self.customer_id)

        value = self.value

        at: str | Unset = UNSET
        if not isinstance(self.at, Unset):
            at = self.at.isoformat()

        subject = self.subject

        properties: dict[str, Any] | Unset = UNSET
        if not isinstance(self.properties, Unset):
            properties = self.properties.to_dict()

        flags: dict[str, Any] | Unset = UNSET
        if not isinstance(self.flags, Unset):
            flags = self.flags.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "customer_id": customer_id,
            }
        )
        if value is not UNSET:
            field_dict["value"] = value
        if at is not UNSET:
            field_dict["at"] = at
        if subject is not UNSET:
            field_dict["subject"] = subject
        if properties is not UNSET:
            field_dict["properties"] = properties
        if flags is not UNSET:
            field_dict["flags"] = flags

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_event_flags import FlagEventFlags
        from ..models.flag_event_properties import FlagEventProperties

        d = dict(src_dict)
        name = d.pop("name")

        customer_id = UUID(d.pop("customer_id"))

        value = d.pop("value", UNSET)

        _at = d.pop("at", UNSET)
        at: datetime.datetime | Unset
        if isinstance(_at, Unset):
            at = UNSET
        else:
            at = datetime.datetime.fromisoformat(_at)

        subject = d.pop("subject", UNSET)

        _properties = d.pop("properties", UNSET)
        properties: FlagEventProperties | Unset
        if isinstance(_properties, Unset):
            properties = UNSET
        else:
            properties = FlagEventProperties.from_dict(_properties)

        _flags = d.pop("flags", UNSET)
        flags: FlagEventFlags | Unset
        if isinstance(_flags, Unset):
            flags = UNSET
        else:
            flags = FlagEventFlags.from_dict(_flags)

        flag_event = cls(
            name=name,
            customer_id=customer_id,
            value=value,
            at=at,
            subject=subject,
            properties=properties,
            flags=flags,
        )

        return flag_event
