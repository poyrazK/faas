from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.delayed_task_after_request_headers import DelayedTaskAfterRequestHeaders
    from ..models.delayed_task_after_request_payload import DelayedTaskAfterRequestPayload
    from ..models.invocation_destinations import InvocationDestinations
    from ..models.retry_policy_dto import RetryPolicyDTO


T = TypeVar("T", bound="DelayedTaskAfterRequest")


@_attrs_define
class DelayedTaskAfterRequest:
    """Schedule a delayed task after a relative whole-second delay."""

    delay_seconds: int
    payload: DelayedTaskAfterRequestPayload | Unset = UNSET
    headers: DelayedTaskAfterRequestHeaders | Unset = UNSET
    method: str | Unset = "POST"
    path: str | Unset = "/"
    retry_policy: RetryPolicyDTO | Unset = UNSET
    """ADR-134 PR-B. Wire shape for dispatch.RetryPolicy. max_attempts
    is a requested total-attempt count; zero inherits the applicable
    account plan and never means unlimited. Durable invocation
    producers materialize the effective plan-capped value, and the
    scheduler re-clamps it at dispatch time to account for later plan
    downgrades. Lives in pkg/api so the SDK can type the policy
    without importing pkg/dispatch directly.
    """
    retention_seconds: int | Unset = UNSET
    destinations: InvocationDestinations | Unset = UNSET
    """EPIC #1278. Optional terminal callbacks for an async invocation. Values are app webhook subscription IDs
    owned by the invoking app."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        delay_seconds = self.delay_seconds

        payload: dict[str, Any] | Unset = UNSET
        if not isinstance(self.payload, Unset):
            payload = self.payload.to_dict()

        headers: dict[str, Any] | Unset = UNSET
        if not isinstance(self.headers, Unset):
            headers = self.headers.to_dict()

        method = self.method

        path = self.path

        retry_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.retry_policy, Unset):
            retry_policy = self.retry_policy.to_dict()

        retention_seconds = self.retention_seconds

        destinations: dict[str, Any] | Unset = UNSET
        if not isinstance(self.destinations, Unset):
            destinations = self.destinations.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "delay_seconds": delay_seconds,
            }
        )
        if payload is not UNSET:
            field_dict["payload"] = payload
        if headers is not UNSET:
            field_dict["headers"] = headers
        if method is not UNSET:
            field_dict["method"] = method
        if path is not UNSET:
            field_dict["path"] = path
        if retry_policy is not UNSET:
            field_dict["retry_policy"] = retry_policy
        if retention_seconds is not UNSET:
            field_dict["retention_seconds"] = retention_seconds
        if destinations is not UNSET:
            field_dict["destinations"] = destinations

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.delayed_task_after_request_headers import DelayedTaskAfterRequestHeaders
        from ..models.delayed_task_after_request_payload import DelayedTaskAfterRequestPayload
        from ..models.invocation_destinations import InvocationDestinations
        from ..models.retry_policy_dto import RetryPolicyDTO

        d = dict(src_dict)
        delay_seconds = d.pop("delay_seconds")

        _payload = d.pop("payload", UNSET)
        payload: DelayedTaskAfterRequestPayload | Unset
        if isinstance(_payload, Unset):
            payload = UNSET
        else:
            payload = DelayedTaskAfterRequestPayload.from_dict(_payload)

        _headers = d.pop("headers", UNSET)
        headers: DelayedTaskAfterRequestHeaders | Unset
        if isinstance(_headers, Unset):
            headers = UNSET
        else:
            headers = DelayedTaskAfterRequestHeaders.from_dict(_headers)

        method = d.pop("method", UNSET)

        path = d.pop("path", UNSET)

        _retry_policy = d.pop("retry_policy", UNSET)
        retry_policy: RetryPolicyDTO | Unset
        if isinstance(_retry_policy, Unset):
            retry_policy = UNSET
        else:
            retry_policy = RetryPolicyDTO.from_dict(_retry_policy)

        retention_seconds = d.pop("retention_seconds", UNSET)

        _destinations = d.pop("destinations", UNSET)
        destinations: InvocationDestinations | Unset
        if isinstance(_destinations, Unset):
            destinations = UNSET
        else:
            destinations = InvocationDestinations.from_dict(_destinations)

        delayed_task_after_request = cls(
            delay_seconds=delay_seconds,
            payload=payload,
            headers=headers,
            method=method,
            path=path,
            retry_policy=retry_policy,
            retention_seconds=retention_seconds,
            destinations=destinations,
        )

        delayed_task_after_request.additional_properties = d
        return delayed_task_after_request

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
