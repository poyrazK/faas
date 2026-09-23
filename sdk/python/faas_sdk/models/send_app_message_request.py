from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.send_app_message_request_data_content_type import (
    SendAppMessageRequestDataContentType,
    check_send_app_message_request_data_content_type,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.retry_policy_dto import RetryPolicyDTO


T = TypeVar("T", bound="SendAppMessageRequest")


@_attrs_define
class SendAppMessageRequest:
    """A CloudEvents-compatible application-inbox message."""

    type_: str
    data: Any
    """Any valid JSON value delivered inside the CloudEvents envelope."""
    id: str | Unset = UNSET
    """Generated when omitted."""
    source: str | Unset = "gregale.send"
    time: datetime.datetime | Unset = UNSET
    """Server time when omitted."""
    data_content_type: SendAppMessageRequestDataContentType | Unset = "application/json"
    queue_name: str | Unset = UNSET
    retry_policy: RetryPolicyDTO | Unset = UNSET
    """ADR-134 PR-B. Wire shape for dispatch.RetryPolicy. max_attempts
    is a requested total-attempt count; zero inherits the applicable
    account plan and never means unlimited. Durable invocation
    producers materialize the effective plan-capped value, and the
    scheduler re-clamps it at dispatch time to account for later plan
    downgrades. Lives in pkg/api so the SDK can type the policy
    without importing pkg/dispatch directly.
    """

    def to_dict(self) -> dict[str, Any]:
        type_ = self.type_

        data = self.data

        id = self.id

        source = self.source

        time: str | Unset = UNSET
        if not isinstance(self.time, Unset):
            time = self.time.isoformat()

        data_content_type: str | Unset = UNSET
        if not isinstance(self.data_content_type, Unset):
            data_content_type = self.data_content_type

        queue_name = self.queue_name

        retry_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.retry_policy, Unset):
            retry_policy = self.retry_policy.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "type": type_,
                "data": data,
            }
        )
        if id is not UNSET:
            field_dict["id"] = id
        if source is not UNSET:
            field_dict["source"] = source
        if time is not UNSET:
            field_dict["time"] = time
        if data_content_type is not UNSET:
            field_dict["data_content_type"] = data_content_type
        if queue_name is not UNSET:
            field_dict["queue_name"] = queue_name
        if retry_policy is not UNSET:
            field_dict["retry_policy"] = retry_policy

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.retry_policy_dto import RetryPolicyDTO

        d = dict(src_dict)
        type_ = d.pop("type")

        data = d.pop("data")

        id = d.pop("id", UNSET)

        source = d.pop("source", UNSET)

        _time = d.pop("time", UNSET)
        time: datetime.datetime | Unset
        if isinstance(_time, Unset):
            time = UNSET
        else:
            time = datetime.datetime.fromisoformat(_time)

        _data_content_type = d.pop("data_content_type", UNSET)
        data_content_type: SendAppMessageRequestDataContentType | Unset
        if isinstance(_data_content_type, Unset):
            data_content_type = UNSET
        else:
            data_content_type = check_send_app_message_request_data_content_type(_data_content_type)

        queue_name = d.pop("queue_name", UNSET)

        _retry_policy = d.pop("retry_policy", UNSET)
        retry_policy: RetryPolicyDTO | Unset
        if isinstance(_retry_policy, Unset):
            retry_policy = UNSET
        else:
            retry_policy = RetryPolicyDTO.from_dict(_retry_policy)

        send_app_message_request = cls(
            type_=type_,
            data=data,
            id=id,
            source=source,
            time=time,
            data_content_type=data_content_type,
            queue_name=queue_name,
            retry_policy=retry_policy,
        )

        return send_app_message_request
