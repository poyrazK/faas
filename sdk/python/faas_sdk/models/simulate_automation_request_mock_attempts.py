from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.automation_simulation_mock_attempt import AutomationSimulationMockAttempt


T = TypeVar("T", bound="SimulateAutomationRequestMockAttempts")


@_attrs_define
class SimulateAutomationRequestMockAttempts:
    """Ordered per-attempt outcomes keyed by action name; waits accept one timeout outcome when they have an on_timeout
    route. Action timeouts also require on_timeout. Cannot be combined with mock_outputs for the same step.

    """

    additional_properties: dict[str, list[AutomationSimulationMockAttempt]] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        for prop_name, prop in self.additional_properties.items():
            field_dict[prop_name] = []
            for additional_property_item_data in prop:
                additional_property_item = additional_property_item_data.to_dict()
                field_dict[prop_name].append(additional_property_item)

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_simulation_mock_attempt import AutomationSimulationMockAttempt

        d = dict(src_dict)
        simulate_automation_request_mock_attempts = cls()

        additional_properties = {}
        for prop_name, prop_dict in d.items():
            additional_property = []
            _additional_property = prop_dict
            for additional_property_item_data in _additional_property:
                additional_property_item = AutomationSimulationMockAttempt.from_dict(additional_property_item_data)

                additional_property.append(additional_property_item)

            additional_properties[prop_name] = additional_property

        simulate_automation_request_mock_attempts.additional_properties = additional_properties
        return simulate_automation_request_mock_attempts

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> list[AutomationSimulationMockAttempt]:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: list[AutomationSimulationMockAttempt]) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
