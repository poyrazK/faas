from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

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

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        customer_id = str(self.customer_id)

        value = self.value

        at: str | Unset = UNSET
        if not isinstance(self.at, Unset):
            at = self.at.isoformat()

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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
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

        flag_event = cls(
            name=name,
            customer_id=customer_id,
            value=value,
            at=at,
        )

        return flag_event
