from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.publish_event_request_data_content_type import (
    PublishEventRequestDataContentType,
    check_publish_event_request_data_content_type,
)
from ..models.publish_event_request_datacontenttype import (
    PublishEventRequestDatacontenttype,
    check_publish_event_request_datacontenttype,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="PublishEventRequest")


@_attrs_define
class PublishEventRequest:
    """Caller-authored envelope for the tenant-scoped internal event router."""

    id: str
    """Caller-chosen idempotent event identifier."""
    source: str
    """Logical producer or service that emitted the event."""
    type_: str
    """Event type used by future content-based matching."""
    data: Any
    """JSON event payload."""
    time: datetime.datetime | Unset = UNSET
    """Event occurrence time; omitted values are stamped at ingress."""
    datacontenttype: PublishEventRequestDatacontenttype | Unset = "application/json"
    data_content_type: PublishEventRequestDataContentType | Unset = UNSET
    schemaversion: str | Unset = UNSET
    """Required once a JSON Schema is registered for this source and type."""
    accountid: UUID | Unset = UNSET
    """Optional tenancy assertion; must match the bearer account."""
    account_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        source = self.source

        type_ = self.type_

        data = self.data

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

        accountid: str | Unset = UNSET
        if not isinstance(self.accountid, Unset):
            accountid = str(self.accountid)

        account_id: str | Unset = UNSET
        if not isinstance(self.account_id, Unset):
            account_id = str(self.account_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "source": source,
                "type": type_,
                "data": data,
            }
        )
        if time is not UNSET:
            field_dict["time"] = time
        if datacontenttype is not UNSET:
            field_dict["datacontenttype"] = datacontenttype
        if data_content_type is not UNSET:
            field_dict["data_content_type"] = data_content_type
        if schemaversion is not UNSET:
            field_dict["schemaversion"] = schemaversion
        if accountid is not UNSET:
            field_dict["accountid"] = accountid
        if account_id is not UNSET:
            field_dict["account_id"] = account_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        source = d.pop("source")

        type_ = d.pop("type")

        data = d.pop("data")

        _time = d.pop("time", UNSET)
        time: datetime.datetime | Unset
        if isinstance(_time, Unset):
            time = UNSET
        else:
            time = datetime.datetime.fromisoformat(_time)

        _datacontenttype = d.pop("datacontenttype", UNSET)
        datacontenttype: PublishEventRequestDatacontenttype | Unset
        if isinstance(_datacontenttype, Unset):
            datacontenttype = UNSET
        else:
            datacontenttype = check_publish_event_request_datacontenttype(_datacontenttype)

        _data_content_type = d.pop("data_content_type", UNSET)
        data_content_type: PublishEventRequestDataContentType | Unset
        if isinstance(_data_content_type, Unset):
            data_content_type = UNSET
        else:
            data_content_type = check_publish_event_request_data_content_type(_data_content_type)

        schemaversion = d.pop("schemaversion", UNSET)

        _accountid = d.pop("accountid", UNSET)
        accountid: UUID | Unset
        if isinstance(_accountid, Unset):
            accountid = UNSET
        else:
            accountid = UUID(_accountid)

        _account_id = d.pop("account_id", UNSET)
        account_id: UUID | Unset
        if isinstance(_account_id, Unset):
            account_id = UNSET
        else:
            account_id = UUID(_account_id)

        publish_event_request = cls(
            id=id,
            source=source,
            type_=type_,
            data=data,
            time=time,
            datacontenttype=datacontenttype,
            data_content_type=data_content_type,
            schemaversion=schemaversion,
            accountid=accountid,
            account_id=account_id,
        )

        return publish_event_request
