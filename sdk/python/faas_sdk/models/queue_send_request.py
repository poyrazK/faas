from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.queue_send_request_payload import QueueSendRequestPayload
    from ..models.retry_policy_dto import RetryPolicyDTO


T = TypeVar("T", bound="QueueSendRequest")


@_attrs_define
class QueueSendRequest:
    """Body for POST /v1/apps/{slug}/queues/send. Cap-checked against MaxQueueDepth."""

    payload: QueueSendRequestPayload | Unset = UNSET
    queue_name: str | Unset = UNSET
    """Optional logical queue name. Required when an app has multiple enabled queue consumers."""
    retry_policy: RetryPolicyDTO | Unset = UNSET
    """ADR-134 PR-B. Wire shape for dispatch.RetryPolicy. max_attempts
    is a requested total-attempt count; zero inherits the applicable
    account plan and never means unlimited. Durable invocation
    producers materialize the effective plan-capped value, and the
    scheduler re-clamps it at dispatch time to account for later plan
    downgrades. Lives in pkg/api so the SDK can type the policy
    without importing pkg/dispatch directly.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        payload: dict[str, Any] | Unset = UNSET
        if not isinstance(self.payload, Unset):
            payload = self.payload.to_dict()

        queue_name = self.queue_name

        retry_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.retry_policy, Unset):
            retry_policy = self.retry_policy.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if payload is not UNSET:
            field_dict["payload"] = payload
        if queue_name is not UNSET:
            field_dict["queue_name"] = queue_name
        if retry_policy is not UNSET:
            field_dict["retry_policy"] = retry_policy

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.queue_send_request_payload import QueueSendRequestPayload
        from ..models.retry_policy_dto import RetryPolicyDTO

        d = dict(src_dict)
        _payload = d.pop("payload", UNSET)
        payload: QueueSendRequestPayload | Unset
        if isinstance(_payload, Unset):
            payload = UNSET
        else:
            payload = QueueSendRequestPayload.from_dict(_payload)

        queue_name = d.pop("queue_name", UNSET)

        _retry_policy = d.pop("retry_policy", UNSET)
        retry_policy: RetryPolicyDTO | Unset
        if isinstance(_retry_policy, Unset):
            retry_policy = UNSET
        else:
            retry_policy = RetryPolicyDTO.from_dict(_retry_policy)

        queue_send_request = cls(
            payload=payload,
            queue_name=queue_name,
            retry_policy=retry_policy,
        )

        queue_send_request.additional_properties = d
        return queue_send_request

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
