from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.queue_receive_response_payload import QueueReceiveResponsePayload
    from ..models.queue_receive_response_result import QueueReceiveResponseResult


T = TypeVar("T", bound="QueueReceiveResponse")


@_attrs_define
class QueueReceiveResponse:
    """200 — a completed queue invocation observed by the long-poll feed. 204 (no body) on timeout."""

    id: str
    payload: QueueReceiveResponsePayload
    environment: str | Unset = UNSET
    """Original environment of the completed invocation in this queue feed."""
    queue_binding_id: str | Unset = UNSET
    """Retained binding identity of the completed queue invocation; absent for legacy work."""
    result: QueueReceiveResponseResult | Unset = UNSET
    trace_id: str | Unset = UNSET
    traceparent: str | Unset = UNSET
    """W3C traceparent propagated from the producer."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        payload = self.payload.to_dict()

        environment = self.environment

        queue_binding_id = self.queue_binding_id

        result: dict[str, Any] | Unset = UNSET
        if not isinstance(self.result, Unset):
            result = self.result.to_dict()

        trace_id = self.trace_id

        traceparent = self.traceparent

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "payload": payload,
            }
        )
        if environment is not UNSET:
            field_dict["environment"] = environment
        if queue_binding_id is not UNSET:
            field_dict["queue_binding_id"] = queue_binding_id
        if result is not UNSET:
            field_dict["result"] = result
        if trace_id is not UNSET:
            field_dict["trace_id"] = trace_id
        if traceparent is not UNSET:
            field_dict["traceparent"] = traceparent

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.queue_receive_response_payload import QueueReceiveResponsePayload
        from ..models.queue_receive_response_result import QueueReceiveResponseResult

        d = dict(src_dict)
        id = d.pop("id")

        payload = QueueReceiveResponsePayload.from_dict(d.pop("payload"))

        environment = d.pop("environment", UNSET)

        queue_binding_id = d.pop("queue_binding_id", UNSET)

        _result = d.pop("result", UNSET)
        result: QueueReceiveResponseResult | Unset
        if isinstance(_result, Unset):
            result = UNSET
        else:
            result = QueueReceiveResponseResult.from_dict(_result)

        trace_id = d.pop("trace_id", UNSET)

        traceparent = d.pop("traceparent", UNSET)

        queue_receive_response = cls(
            id=id,
            payload=payload,
            environment=environment,
            queue_binding_id=queue_binding_id,
            result=result,
            trace_id=trace_id,
            traceparent=traceparent,
        )

        queue_receive_response.additional_properties = d
        return queue_receive_response

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
