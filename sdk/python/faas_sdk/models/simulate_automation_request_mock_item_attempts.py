from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.simulate_automation_request_mock_item_attempts_additional_property import (
        SimulateAutomationRequestMockItemAttemptsAdditionalProperty,
    )


T = TypeVar("T", bound="SimulateAutomationRequestMockItemAttempts")


@_attrs_define
class SimulateAutomationRequestMockItemAttempts:
    """Per-item attempt outcomes keyed by for_each root name and canonical zero-based item index (0..127). Sparse indexes
    are allowed but must exist in the materialized collection. Cannot be combined with mock_item_outputs for the same
    loop. Item timeouts require an action timeout and follow its retry policy. Terminal item failures stop later items
    unless on_item_failure is continue; the loop remains failed or dead, with null placeholders for failed items when
    continuing.

    """

    additional_properties: dict[str, SimulateAutomationRequestMockItemAttemptsAdditionalProperty] = _attrs_field(
        init=False, factory=dict
    )

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        for prop_name, prop in self.additional_properties.items():
            field_dict[prop_name] = prop.to_dict()

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.simulate_automation_request_mock_item_attempts_additional_property import (
            SimulateAutomationRequestMockItemAttemptsAdditionalProperty,
        )

        d = dict(src_dict)
        simulate_automation_request_mock_item_attempts = cls()

        additional_properties = {}
        for prop_name, prop_dict in d.items():
            additional_property = SimulateAutomationRequestMockItemAttemptsAdditionalProperty.from_dict(prop_dict)

            additional_properties[prop_name] = additional_property

        simulate_automation_request_mock_item_attempts.additional_properties = additional_properties
        return simulate_automation_request_mock_item_attempts

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> SimulateAutomationRequestMockItemAttemptsAdditionalProperty:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: SimulateAutomationRequestMockItemAttemptsAdditionalProperty) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
