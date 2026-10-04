from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.exclusive_operation_request_invocation_headers import ExclusiveOperationRequestInvocationHeaders
    from ..models.exclusive_operation_request_invocation_payload import ExclusiveOperationRequestInvocationPayload


T = TypeVar("T", bound="ExclusiveOperationRequestInvocation")


@_attrs_define
class ExclusiveOperationRequestInvocation:
    payload: ExclusiveOperationRequestInvocationPayload | Unset = UNSET
    headers: ExclusiveOperationRequestInvocationHeaders | Unset = UNSET
    method: str | Unset = "POST"
    path: str | Unset = "/"

    def to_dict(self) -> dict[str, Any]:
        payload: dict[str, Any] | Unset = UNSET
        if not isinstance(self.payload, Unset):
            payload = self.payload.to_dict()

        headers: dict[str, Any] | Unset = UNSET
        if not isinstance(self.headers, Unset):
            headers = self.headers.to_dict()

        method = self.method

        path = self.path

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if payload is not UNSET:
            field_dict["payload"] = payload
        if headers is not UNSET:
            field_dict["headers"] = headers
        if method is not UNSET:
            field_dict["method"] = method
        if path is not UNSET:
            field_dict["path"] = path

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.exclusive_operation_request_invocation_headers import ExclusiveOperationRequestInvocationHeaders
        from ..models.exclusive_operation_request_invocation_payload import ExclusiveOperationRequestInvocationPayload

        d = dict(src_dict)
        _payload = d.pop("payload", UNSET)
        payload: ExclusiveOperationRequestInvocationPayload | Unset
        if isinstance(_payload, Unset):
            payload = UNSET
        else:
            payload = ExclusiveOperationRequestInvocationPayload.from_dict(_payload)

        _headers = d.pop("headers", UNSET)
        headers: ExclusiveOperationRequestInvocationHeaders | Unset
        if isinstance(_headers, Unset):
            headers = UNSET
        else:
            headers = ExclusiveOperationRequestInvocationHeaders.from_dict(_headers)

        method = d.pop("method", UNSET)

        path = d.pop("path", UNSET)

        exclusive_operation_request_invocation = cls(
            payload=payload,
            headers=headers,
            method=method,
            path=path,
        )

        return exclusive_operation_request_invocation
