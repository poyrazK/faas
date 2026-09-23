from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.preview_event_request_data_content_type import (
    PreviewEventRequestDataContentType,
    check_preview_event_request_data_content_type,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="PreviewEventRequest")


@_attrs_define
class PreviewEventRequest:
    """Event fields to evaluate without persisting or delivering the event."""

    source: str
    type_: str
    data: Any
    """JSON event payload evaluated by subscription filters."""
    id: str | Unset = UNSET
    """Optional event id used when filters inspect the CloudEvents id."""
    time: datetime.datetime | Unset = UNSET
    """Event occurrence time; omitted values use the current time."""
    data_content_type: PreviewEventRequestDataContentType | Unset = "application/json"

    def to_dict(self) -> dict[str, Any]:
        source = self.source

        type_ = self.type_

        data = self.data

        id = self.id

        time: str | Unset = UNSET
        if not isinstance(self.time, Unset):
            time = self.time.isoformat()

        data_content_type: str | Unset = UNSET
        if not isinstance(self.data_content_type, Unset):
            data_content_type = self.data_content_type

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "source": source,
                "type": type_,
                "data": data,
            }
        )
        if id is not UNSET:
            field_dict["id"] = id
        if time is not UNSET:
            field_dict["time"] = time
        if data_content_type is not UNSET:
            field_dict["data_content_type"] = data_content_type

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source = d.pop("source")

        type_ = d.pop("type")

        data = d.pop("data")

        id = d.pop("id", UNSET)

        _time = d.pop("time", UNSET)
        time: datetime.datetime | Unset
        if isinstance(_time, Unset):
            time = UNSET
        else:
            time = datetime.datetime.fromisoformat(_time)

        _data_content_type = d.pop("data_content_type", UNSET)
        data_content_type: PreviewEventRequestDataContentType | Unset
        if isinstance(_data_content_type, Unset):
            data_content_type = UNSET
        else:
            data_content_type = check_preview_event_request_data_content_type(_data_content_type)

        preview_event_request = cls(
            source=source,
            type_=type_,
            data=data,
            id=id,
            time=time,
            data_content_type=data_content_type,
        )

        return preview_event_request
