from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_step_spec import WorkflowStepSpec
    from ..models.workflow_trigger_spec import WorkflowTriggerSpec


T = TypeVar("T", bound="WorkflowSpec")


@_attrs_define
class WorkflowSpec:
    """A named workflow DAG submitted with a deployment (ADR-081)."""

    name: str
    steps: list[WorkflowStepSpec]
    trigger: None | Unset | WorkflowTriggerSpec = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.workflow_trigger_spec import WorkflowTriggerSpec

        name = self.name

        steps = []
        for steps_item_data in self.steps:
            steps_item = steps_item_data.to_dict()
            steps.append(steps_item)

        trigger: dict[str, Any] | None | Unset
        if isinstance(self.trigger, Unset):
            trigger = UNSET
        elif isinstance(self.trigger, WorkflowTriggerSpec):
            trigger = self.trigger.to_dict()
        else:
            trigger = self.trigger

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "steps": steps,
            }
        )
        if trigger is not UNSET:
            field_dict["trigger"] = trigger

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_step_spec import WorkflowStepSpec
        from ..models.workflow_trigger_spec import WorkflowTriggerSpec

        d = dict(src_dict)
        name = d.pop("name")

        steps = []
        _steps = d.pop("steps")
        for steps_item_data in _steps:
            steps_item = WorkflowStepSpec.from_dict(steps_item_data)

            steps.append(steps_item)

        def _parse_trigger(data: object) -> None | Unset | WorkflowTriggerSpec:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                trigger_type_0 = WorkflowTriggerSpec.from_dict(data)

                return trigger_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | Unset | WorkflowTriggerSpec, data)

        trigger = _parse_trigger(d.pop("trigger", UNSET))

        workflow_spec = cls(
            name=name,
            steps=steps,
            trigger=trigger,
        )

        workflow_spec.additional_properties = d
        return workflow_spec

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
