from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="DataPipelineFreshness")


@_attrs_define
class DataPipelineFreshness:
    """Read-time confirmation of an applied checkpoint against the owner freshness target. Old confirmation is not measured
    replication lag; an idle source may already be caught up.

    """

    target_seconds: int
    """DataPipelineFreshness uses zero to disable confirmation alerts; enabled targets must be at least 15 seconds."""
    state: str
    message: str
    confirmed_through_at: datetime.datetime | Unset = UNSET
    confirmation_age_seconds: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        target_seconds = self.target_seconds

        state = self.state

        message = self.message

        confirmed_through_at: str | Unset = UNSET
        if not isinstance(self.confirmed_through_at, Unset):
            confirmed_through_at = self.confirmed_through_at.isoformat()

        confirmation_age_seconds = self.confirmation_age_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "target_seconds": target_seconds,
                "state": state,
                "message": message,
            }
        )
        if confirmed_through_at is not UNSET:
            field_dict["confirmed_through_at"] = confirmed_through_at
        if confirmation_age_seconds is not UNSET:
            field_dict["confirmation_age_seconds"] = confirmation_age_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        target_seconds = d.pop("target_seconds")

        state = d.pop("state")

        message = d.pop("message")

        _confirmed_through_at = d.pop("confirmed_through_at", UNSET)
        confirmed_through_at: datetime.datetime | Unset
        if isinstance(_confirmed_through_at, Unset):
            confirmed_through_at = UNSET
        else:
            confirmed_through_at = datetime.datetime.fromisoformat(_confirmed_through_at)

        confirmation_age_seconds = d.pop("confirmation_age_seconds", UNSET)

        data_pipeline_freshness = cls(
            target_seconds=target_seconds,
            state=state,
            message=message,
            confirmed_through_at=confirmed_through_at,
            confirmation_age_seconds=confirmation_age_seconds,
        )

        return data_pipeline_freshness
