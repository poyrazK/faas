from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.queue_send_batch_message import QueueSendBatchMessage
    from ..models.retry_policy_dto import RetryPolicyDTO


T = TypeVar("T", bound="QueueSendBatchRequest")


@_attrs_define
class QueueSendBatchRequest:
    """Body for POST /v1/apps/{slug}/queues/send-batch: up to 100 messages for one queue, sent in order. The body is at
    most 8 MiB; each message meets its plan's message size.

    """

    messages: list[QueueSendBatchMessage]
    environment: str | Unset = UNSET
    """Target registered environment with an enabled scoped queue binding. Omission sends through the default
    environment."""
    queue_name: str | Unset = UNSET
    """Optional logical queue name. Required when an app has multiple enabled queue consumers."""
    retry_policy: RetryPolicyDTO | Unset = UNSET
    """Wire shape for dispatch.RetryPolicy. max_attempts
    is a requested total-attempt count; zero inherits the applicable
    account plan and never means unlimited. Durable invocation
    producers materialize the effective plan-capped value, and the
    scheduler re-clamps it at dispatch time to account for later plan
    downgrades.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        messages = []
        for messages_item_data in self.messages:
            messages_item = messages_item_data.to_dict()
            messages.append(messages_item)

        environment = self.environment

        queue_name = self.queue_name

        retry_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.retry_policy, Unset):
            retry_policy = self.retry_policy.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "messages": messages,
            }
        )
        if environment is not UNSET:
            field_dict["environment"] = environment
        if queue_name is not UNSET:
            field_dict["queue_name"] = queue_name
        if retry_policy is not UNSET:
            field_dict["retry_policy"] = retry_policy

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.queue_send_batch_message import QueueSendBatchMessage
        from ..models.retry_policy_dto import RetryPolicyDTO

        d = dict(src_dict)
        messages = []
        _messages = d.pop("messages")
        for messages_item_data in _messages:
            messages_item = QueueSendBatchMessage.from_dict(messages_item_data)

            messages.append(messages_item)

        environment = d.pop("environment", UNSET)

        queue_name = d.pop("queue_name", UNSET)

        _retry_policy = d.pop("retry_policy", UNSET)
        retry_policy: RetryPolicyDTO | Unset
        if isinstance(_retry_policy, Unset):
            retry_policy = UNSET
        else:
            retry_policy = RetryPolicyDTO.from_dict(_retry_policy)

        queue_send_batch_request = cls(
            messages=messages,
            environment=environment,
            queue_name=queue_name,
            retry_policy=retry_policy,
        )

        queue_send_batch_request.additional_properties = d
        return queue_send_batch_request

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
