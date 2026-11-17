from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.simulate_stored_workflow_request_mock_attempts import SimulateStoredWorkflowRequestMockAttempts
    from ..models.simulate_stored_workflow_request_mock_item_attempts import (
        SimulateStoredWorkflowRequestMockItemAttempts,
    )
    from ..models.simulate_stored_workflow_request_mock_item_outputs import SimulateStoredWorkflowRequestMockItemOutputs
    from ..models.simulate_stored_workflow_request_mock_outputs import SimulateStoredWorkflowRequestMockOutputs
    from ..models.workflow_spec import WorkflowSpec


T = TypeVar("T", bound="SimulateStoredWorkflowRequest")


@_attrs_define
class SimulateStoredWorkflowRequest:
    """Sample workflow data and mocked action, event/callback payload or timeout outcomes for a stateless simulation."""

    definition: WorkflowSpec
    """A named workflow DAG submitted with a deployment. max_concurrent_runs caps active run instances for this
    workflow; excess admitted runs remain pending until a slot opens, subject to the app plan's run quota.
    max_concurrent_actions caps active executor steps across runs of this workflow; steps wait in the scheduler
    queue while all action slots are occupied."""
    input_: Any | Unset = UNSET
    """Workflow input as any JSON value; omission is equivalent to null."""
    mock_outputs: SimulateStoredWorkflowRequestMockOutputs | Unset = UNSET
    """Successful action outputs keyed by root step name; explicit null is a supplied result."""
    mock_item_outputs: SimulateStoredWorkflowRequestMockItemOutputs | Unset = UNSET
    """Ordered successful output prefix keyed by for_each root name; waits and controls cannot be mocked."""
    mock_item_attempts: SimulateStoredWorkflowRequestMockItemAttempts | Unset = UNSET
    """Per-item attempt outcomes keyed by for_each root name and canonical zero-based item index (0..127). Sparse
    indexes are allowed but must exist in the materialized collection. Cannot be combined with mock_item_outputs for
    the same loop. Item timeouts require an action timeout and follow its retry policy. Terminal item failures stop
    later items unless on_item_failure is continue; the loop remains failed or dead, with null placeholders for
    failed items when continuing."""
    mock_attempts: SimulateStoredWorkflowRequestMockAttempts | Unset = UNSET
    """Ordered per-attempt outcomes keyed by action or wait name. Event and callback waits accept exactly one
    success with the received payload as output, or one timeout with a configured timeout and on_timeout route.
    Other waits accept timeout only. Successful wait payloads cannot be the reserved timeout sentinel. Action
    timeouts require on_timeout. Cannot be combined with mock_outputs for the same step."""

    def to_dict(self) -> dict[str, Any]:
        definition = self.definition.to_dict()

        input_ = self.input_

        mock_outputs: dict[str, Any] | Unset = UNSET
        if not isinstance(self.mock_outputs, Unset):
            mock_outputs = self.mock_outputs.to_dict()

        mock_item_outputs: dict[str, Any] | Unset = UNSET
        if not isinstance(self.mock_item_outputs, Unset):
            mock_item_outputs = self.mock_item_outputs.to_dict()

        mock_item_attempts: dict[str, Any] | Unset = UNSET
        if not isinstance(self.mock_item_attempts, Unset):
            mock_item_attempts = self.mock_item_attempts.to_dict()

        mock_attempts: dict[str, Any] | Unset = UNSET
        if not isinstance(self.mock_attempts, Unset):
            mock_attempts = self.mock_attempts.to_dict()

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
        if mock_item_attempts is not UNSET:
            field_dict["mock_item_attempts"] = mock_item_attempts
        if mock_attempts is not UNSET:
            field_dict["mock_attempts"] = mock_attempts

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.simulate_stored_workflow_request_mock_attempts import SimulateStoredWorkflowRequestMockAttempts
        from ..models.simulate_stored_workflow_request_mock_item_attempts import (
            SimulateStoredWorkflowRequestMockItemAttempts,
        )
        from ..models.simulate_stored_workflow_request_mock_item_outputs import (
            SimulateStoredWorkflowRequestMockItemOutputs,
        )
        from ..models.simulate_stored_workflow_request_mock_outputs import SimulateStoredWorkflowRequestMockOutputs
        from ..models.workflow_spec import WorkflowSpec

        d = dict(src_dict)
        definition = WorkflowSpec.from_dict(d.pop("definition"))

        input_ = d.pop("input", UNSET)

        _mock_outputs = d.pop("mock_outputs", UNSET)
        mock_outputs: SimulateStoredWorkflowRequestMockOutputs | Unset
        if isinstance(_mock_outputs, Unset):
            mock_outputs = UNSET
        else:
            mock_outputs = SimulateStoredWorkflowRequestMockOutputs.from_dict(_mock_outputs)

        _mock_item_outputs = d.pop("mock_item_outputs", UNSET)
        mock_item_outputs: SimulateStoredWorkflowRequestMockItemOutputs | Unset
        if isinstance(_mock_item_outputs, Unset):
            mock_item_outputs = UNSET
        else:
            mock_item_outputs = SimulateStoredWorkflowRequestMockItemOutputs.from_dict(_mock_item_outputs)

        _mock_item_attempts = d.pop("mock_item_attempts", UNSET)
        mock_item_attempts: SimulateStoredWorkflowRequestMockItemAttempts | Unset
        if isinstance(_mock_item_attempts, Unset):
            mock_item_attempts = UNSET
        else:
            mock_item_attempts = SimulateStoredWorkflowRequestMockItemAttempts.from_dict(_mock_item_attempts)

        _mock_attempts = d.pop("mock_attempts", UNSET)
        mock_attempts: SimulateStoredWorkflowRequestMockAttempts | Unset
        if isinstance(_mock_attempts, Unset):
            mock_attempts = UNSET
        else:
            mock_attempts = SimulateStoredWorkflowRequestMockAttempts.from_dict(_mock_attempts)

        simulate_stored_workflow_request = cls(
            definition=definition,
            input_=input_,
            mock_outputs=mock_outputs,
            mock_item_outputs=mock_item_outputs,
            mock_item_attempts=mock_item_attempts,
            mock_attempts=mock_attempts,
        )

        return simulate_stored_workflow_request
