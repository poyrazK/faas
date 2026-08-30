from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.invoke_request_headers import InvokeRequestHeaders
    from ..models.invoke_request_payload import InvokeRequestPayload
    from ..models.invoke_request_retry_policy_type_0 import InvokeRequestRetryPolicyType0


T = TypeVar("T", bound="InvokeRequest")


@_attrs_define
class InvokeRequest:
    """Body for POST /v1/apps/{slug}/invoke[/async]. Method defaults to POST; path defaults to `/`."""

    payload: InvokeRequestPayload | Unset = UNSET
    headers: InvokeRequestHeaders | Unset = UNSET
    method: str | Unset = "POST"
    path: str | Unset = "/"
    deadline_at: datetime.datetime | None | Unset = UNSET
    """ADR-134 PR-B. Hard-stop timestamp. Must be within now+Limits.MaxAsyncInvocationDeadlineSeconds."""
    retry_policy: InvokeRequestRetryPolicyType0 | None | Unset = UNSET
    """ADR-134 PR-B. Per-row retry curve override. Shape mirrors dispatch.RetryPolicy: { max_attempts,
    base_seconds, max_seconds, jitter_seconds }."""
    retention_seconds: int | None | Unset = UNSET
    """ADR-134 PR-B. Retention horizon in seconds. NULL/0 means 'use plan default'
    (Limits.MaxAsyncResultRetentionSeconds)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.invoke_request_retry_policy_type_0 import InvokeRequestRetryPolicyType0

        payload: dict[str, Any] | Unset = UNSET
        if not isinstance(self.payload, Unset):
            payload = self.payload.to_dict()

        headers: dict[str, Any] | Unset = UNSET
        if not isinstance(self.headers, Unset):
            headers = self.headers.to_dict()

        method = self.method

        path = self.path

        deadline_at: None | str | Unset
        if isinstance(self.deadline_at, Unset):
            deadline_at = UNSET
        elif isinstance(self.deadline_at, datetime.datetime):
            deadline_at = self.deadline_at.isoformat()
        else:
            deadline_at = self.deadline_at

        retry_policy: dict[str, Any] | None | Unset
        if isinstance(self.retry_policy, Unset):
            retry_policy = UNSET
        elif isinstance(self.retry_policy, InvokeRequestRetryPolicyType0):
            retry_policy = self.retry_policy.to_dict()
        else:
            retry_policy = self.retry_policy

        retention_seconds: int | None | Unset
        if isinstance(self.retention_seconds, Unset):
            retention_seconds = UNSET
        else:
            retention_seconds = self.retention_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if payload is not UNSET:
            field_dict["payload"] = payload
        if headers is not UNSET:
            field_dict["headers"] = headers
        if method is not UNSET:
            field_dict["method"] = method
        if path is not UNSET:
            field_dict["path"] = path
        if deadline_at is not UNSET:
            field_dict["deadline_at"] = deadline_at
        if retry_policy is not UNSET:
            field_dict["retry_policy"] = retry_policy
        if retention_seconds is not UNSET:
            field_dict["retention_seconds"] = retention_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.invoke_request_headers import InvokeRequestHeaders
        from ..models.invoke_request_payload import InvokeRequestPayload
        from ..models.invoke_request_retry_policy_type_0 import InvokeRequestRetryPolicyType0

        d = dict(src_dict)
        _payload = d.pop("payload", UNSET)
        payload: InvokeRequestPayload | Unset
        if isinstance(_payload, Unset):
            payload = UNSET
        else:
            payload = InvokeRequestPayload.from_dict(_payload)

        _headers = d.pop("headers", UNSET)
        headers: InvokeRequestHeaders | Unset
        if isinstance(_headers, Unset):
            headers = UNSET
        else:
            headers = InvokeRequestHeaders.from_dict(_headers)

        method = d.pop("method", UNSET)

        path = d.pop("path", UNSET)

        def _parse_deadline_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                deadline_at_type_0 = datetime.datetime.fromisoformat(data)

                return deadline_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        deadline_at = _parse_deadline_at(d.pop("deadline_at", UNSET))

        def _parse_retry_policy(data: object) -> InvokeRequestRetryPolicyType0 | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                retry_policy_type_0 = InvokeRequestRetryPolicyType0.from_dict(data)

                return retry_policy_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(InvokeRequestRetryPolicyType0 | None | Unset, data)

        retry_policy = _parse_retry_policy(d.pop("retry_policy", UNSET))

        def _parse_retention_seconds(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        retention_seconds = _parse_retention_seconds(d.pop("retention_seconds", UNSET))

        invoke_request = cls(
            payload=payload,
            headers=headers,
            method=method,
            path=path,
            deadline_at=deadline_at,
            retry_policy=retry_policy,
            retention_seconds=retention_seconds,
        )

        invoke_request.additional_properties = d
        return invoke_request

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
