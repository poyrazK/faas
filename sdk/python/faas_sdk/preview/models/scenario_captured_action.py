from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scenario_captured_action_source import ScenarioCapturedActionSource, check_scenario_captured_action_source
from ..types import UNSET, Unset

T = TypeVar("T", bound="ScenarioCapturedAction")


@_attrs_define
class ScenarioCapturedAction:
    """A managed operation or workflow webhook intent captured at completion. Runtime IDs support inspection; comparison
    uses logical workloads and committed business data.

    """

    id: UUID
    workload: str
    source: ScenarioCapturedActionSource
    operation_id: UUID
    name: str
    webhook_id: UUID
    receiver_sha256: str
    """Fingerprint of destination scope, business tenant reference, exact URL, delivery format and retry policy
    observed at completion. URLs and secrets are not exported."""
    type_: str
    payload: Any
    """Committed JSON business data bounded to 64 KiB per intent."""
    workflow: str | Unset = UNSET
    step: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        workload = self.workload

        source: str = self.source

        operation_id = str(self.operation_id)

        name = self.name

        webhook_id = str(self.webhook_id)

        receiver_sha256 = self.receiver_sha256

        type_ = self.type_

        payload = self.payload

        workflow = self.workflow

        step = self.step

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "workload": workload,
                "source": source,
                "operation_id": operation_id,
                "name": name,
                "webhook_id": webhook_id,
                "receiver_sha256": receiver_sha256,
                "type": type_,
                "payload": payload,
            }
        )
        if workflow is not UNSET:
            field_dict["workflow"] = workflow
        if step is not UNSET:
            field_dict["step"] = step

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        workload = d.pop("workload")

        source = check_scenario_captured_action_source(d.pop("source"))

        operation_id = UUID(d.pop("operation_id"))

        name = d.pop("name")

        webhook_id = UUID(d.pop("webhook_id"))

        receiver_sha256 = d.pop("receiver_sha256")

        type_ = d.pop("type")

        payload = d.pop("payload")

        workflow = d.pop("workflow", UNSET)

        step = d.pop("step", UNSET)

        scenario_captured_action = cls(
            id=id,
            workload=workload,
            source=source,
            operation_id=operation_id,
            name=name,
            webhook_id=webhook_id,
            receiver_sha256=receiver_sha256,
            type_=type_,
            payload=payload,
            workflow=workflow,
            step=step,
        )

        scenario_captured_action.additional_properties = d
        return scenario_captured_action

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
