from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.preview_event_request_data_content_type import (
    PreviewEventRequestDataContentType,
    check_preview_event_request_data_content_type,
)
from ..models.preview_event_request_datacontenttype import (
    PreviewEventRequestDatacontenttype,
    check_preview_event_request_datacontenttype,
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
    datacontenttype: PreviewEventRequestDatacontenttype | Unset = "application/json"
    data_content_type: PreviewEventRequestDataContentType | Unset = UNSET
    schemaversion: str | Unset = UNSET
    """Validated against the registered JSON Schema when one exists."""

    def to_dict(self) -> dict[str, Any]:
        source = self.source

        type_ = self.type_

        data = self.data

        id = self.id

        time: str | Unset = UNSET
        if not isinstance(self.time, Unset):
            time = self.time.isoformat()

        datacontenttype: str | Unset = UNSET
        if not isinstance(self.datacontenttype, Unset):
            datacontenttype = self.datacontenttype

        data_content_type: str | Unset = UNSET
        if not isinstance(self.data_content_type, Unset):
            data_content_type = self.data_content_type

        schemaversion = self.schemaversion

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
        if datacontenttype is not UNSET:
            field_dict["datacontenttype"] = datacontenttype
        if data_content_type is not UNSET:
            field_dict["data_content_type"] = data_content_type
        if schemaversion is not UNSET:
            field_dict["schemaversion"] = schemaversion

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

        _datacontenttype = d.pop("datacontenttype", UNSET)
        datacontenttype: PreviewEventRequestDatacontenttype | Unset
        if isinstance(_datacontenttype, Unset):
            datacontenttype = UNSET
        else:
            datacontenttype = check_preview_event_request_datacontenttype(_datacontenttype)

        _data_content_type = d.pop("data_content_type", UNSET)
        data_content_type: PreviewEventRequestDataContentType | Unset
        if isinstance(_data_content_type, Unset):
            data_content_type = UNSET
        else:
            data_content_type = check_preview_event_request_data_content_type(_data_content_type)

        schemaversion = d.pop("schemaversion", UNSET)

        preview_event_request = cls(
            source=source,
            type_=type_,
            data=data,
            id=id,
            time=time,
            datacontenttype=datacontenttype,
            data_content_type=data_content_type,
            schemaversion=schemaversion,
        )

        return preview_event_request
