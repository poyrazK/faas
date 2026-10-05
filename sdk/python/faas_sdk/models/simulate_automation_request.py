from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.simulate_automation_request_mock_item_outputs import SimulateAutomationRequestMockItemOutputs
    from ..models.simulate_automation_request_mock_outputs import SimulateAutomationRequestMockOutputs
    from ..models.workflow_spec import WorkflowSpec


T = TypeVar("T", bound="SimulateAutomationRequest")


@_attrs_define
class SimulateAutomationRequest:
    """Sample workflow data and successful action results for a stateless simulation."""

    definition: WorkflowSpec
    """A named workflow DAG submitted with a deployment (ADR-081)."""
    input_: Any | Unset = UNSET
    """Workflow input as any JSON value; omission is equivalent to null."""
    mock_outputs: SimulateAutomationRequestMockOutputs | Unset = UNSET
    """Successful action outputs keyed by root step name; explicit null is a supplied result."""
    mock_item_outputs: SimulateAutomationRequestMockItemOutputs | Unset = UNSET
    """Ordered successful output prefix keyed by for_each root name; waits and controls cannot be mocked."""

    def to_dict(self) -> dict[str, Any]:
        definition = self.definition.to_dict()

        input_ = self.input_

        mock_outputs: dict[str, Any] | Unset = UNSET
        if not isinstance(self.mock_outputs, Unset):
            mock_outputs = self.mock_outputs.to_dict()

        mock_item_outputs: dict[str, Any] | Unset = UNSET
        if not isinstance(self.mock_item_outputs, Unset):
            mock_item_outputs = self.mock_item_outputs.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "definition": definition,
            }
        )
        if input_ is not UNSET:
            field_dict["input"] = input_
        if mock_outputs is not UNSET:
            field_dict["mock_outputs"] = mock_outputs
        if mock_item_outputs is not UNSET:
            field_dict["mock_item_outputs"] = mock_item_outputs

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.simulate_automation_request_mock_item_outputs import SimulateAutomationRequestMockItemOutputs
        from ..models.simulate_automation_request_mock_outputs import SimulateAutomationRequestMockOutputs
        from ..models.workflow_spec import WorkflowSpec

        d = dict(src_dict)
        definition = WorkflowSpec.from_dict(d.pop("definition"))

        input_ = d.pop("input", UNSET)

        _mock_outputs = d.pop("mock_outputs", UNSET)
        mock_outputs: SimulateAutomationRequestMockOutputs | Unset
        if isinstance(_mock_outputs, Unset):
            mock_outputs = UNSET
        else:
            mock_outputs = SimulateAutomationRequestMockOutputs.from_dict(_mock_outputs)

        _mock_item_outputs = d.pop("mock_item_outputs", UNSET)
        mock_item_outputs: SimulateAutomationRequestMockItemOutputs | Unset
        if isinstance(_mock_item_outputs, Unset):
            mock_item_outputs = UNSET
        else:
            mock_item_outputs = SimulateAutomationRequestMockItemOutputs.from_dict(_mock_item_outputs)

        simulate_automation_request = cls(
            definition=definition,
            input_=input_,
            mock_outputs=mock_outputs,
            mock_item_outputs=mock_item_outputs,
        )

        return simulate_automation_request
