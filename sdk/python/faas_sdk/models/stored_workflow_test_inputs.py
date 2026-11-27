from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.stored_workflow_test_inputs_mock_attempts import StoredWorkflowTestInputsMockAttempts
    from ..models.stored_workflow_test_inputs_mock_item_attempts import StoredWorkflowTestInputsMockItemAttempts
    from ..models.stored_workflow_test_inputs_mock_item_outputs import StoredWorkflowTestInputsMockItemOutputs
    from ..models.stored_workflow_test_inputs_mock_outputs import StoredWorkflowTestInputsMockOutputs


T = TypeVar("T", bound="StoredWorkflowTestInputs")


@_attrs_define
class StoredWorkflowTestInputs:
    """Saved test data — Explicitly saved synthetic inputs and mocks. Runs always bind the current saved draft."""

    input_: Any | Unset = UNSET
    """Saved test data — Workflow input as any JSON value; omission is equivalent to null."""
    mock_outputs: StoredWorkflowTestInputsMockOutputs | Unset = UNSET
    """Saved test data — Successful action outputs keyed by root step name; explicit null is a supplied result."""
    mock_item_outputs: StoredWorkflowTestInputsMockItemOutputs | Unset = UNSET
    """Saved test data — Ordered successful output prefix keyed by for_each root name; waits and controls cannot be
    mocked."""
    mock_item_attempts: StoredWorkflowTestInputsMockItemAttempts | Unset = UNSET
    """Saved test data — Per-item attempt outcomes keyed by for_each root name and canonical zero-based item index
    (0..127). Sparse indexes are allowed but must exist in the materialized collection. Cannot be combined with
    mock_item_outputs for the same loop. Item timeouts require an action timeout and follow its retry policy.
    Terminal item failures stop later items unless on_item_failure is continue; the loop remains failed or dead,
    with null placeholders for failed items when continuing."""
    mock_attempts: StoredWorkflowTestInputsMockAttempts | Unset = UNSET
    """Saved test data — Ordered per-attempt outcomes keyed by action or wait name. Event and callback waits accept
    exactly one success with the received payload as output, or one timeout with a configured timeout and on_timeout
    route. Other waits accept timeout only. Successful wait payloads cannot be the reserved timeout sentinel. Action
    timeouts require on_timeout. Cannot be combined with mock_outputs for the same step."""

    def to_dict(self) -> dict[str, Any]:
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

        field_dict.update({})
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
        from ..models.stored_workflow_test_inputs_mock_attempts import StoredWorkflowTestInputsMockAttempts
        from ..models.stored_workflow_test_inputs_mock_item_attempts import StoredWorkflowTestInputsMockItemAttempts
        from ..models.stored_workflow_test_inputs_mock_item_outputs import StoredWorkflowTestInputsMockItemOutputs
        from ..models.stored_workflow_test_inputs_mock_outputs import StoredWorkflowTestInputsMockOutputs

        d = dict(src_dict)
        input_ = d.pop("input", UNSET)

        _mock_outputs = d.pop("mock_outputs", UNSET)
        mock_outputs: StoredWorkflowTestInputsMockOutputs | Unset
        if isinstance(_mock_outputs, Unset):
            mock_outputs = UNSET
        else:
            mock_outputs = StoredWorkflowTestInputsMockOutputs.from_dict(_mock_outputs)

        _mock_item_outputs = d.pop("mock_item_outputs", UNSET)
        mock_item_outputs: StoredWorkflowTestInputsMockItemOutputs | Unset
        if isinstance(_mock_item_outputs, Unset):
            mock_item_outputs = UNSET
        else:
            mock_item_outputs = StoredWorkflowTestInputsMockItemOutputs.from_dict(_mock_item_outputs)

        _mock_item_attempts = d.pop("mock_item_attempts", UNSET)
        mock_item_attempts: StoredWorkflowTestInputsMockItemAttempts | Unset
        if isinstance(_mock_item_attempts, Unset):
            mock_item_attempts = UNSET
        else:
            mock_item_attempts = StoredWorkflowTestInputsMockItemAttempts.from_dict(_mock_item_attempts)

        _mock_attempts = d.pop("mock_attempts", UNSET)
        mock_attempts: StoredWorkflowTestInputsMockAttempts | Unset
        if isinstance(_mock_attempts, Unset):
            mock_attempts = UNSET
        else:
            mock_attempts = StoredWorkflowTestInputsMockAttempts.from_dict(_mock_attempts)

        stored_workflow_test_inputs = cls(
            input_=input_,
            mock_outputs=mock_outputs,
            mock_item_outputs=mock_item_outputs,
            mock_item_attempts=mock_item_attempts,
            mock_attempts=mock_attempts,
        )

        return stored_workflow_test_inputs
