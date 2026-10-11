from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.workflow_for_each_spec_on_item_failure import (
    WorkflowForEachSpecOnItemFailure,
    check_workflow_for_each_spec_on_item_failure,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_for_each_action_spec import WorkflowForEachActionSpec


T = TypeVar("T", bound="WorkflowForEachSpec")


@_attrs_define
class WorkflowForEachSpec:
    """Bounded-concurrency action over a JSON array from input or a direct dependency
    output. Snapshots all items and resolved action inputs before dispatch. At most
    128 items, 1 MiB source/prepared inputs and 1 MiB collected output. Parent names
    permit at most 64 UTF-8 bytes. Omitted or zero max_parallel means one active
    item; values up to 16 limit active items per batch. Items are admitted in
    input order within a bounded window, and collected output always follows input
    order. By default no new items start after a terminal item failure; already
    active items finish and the parent fails. `on_item_failure: continue` attempts
    later items and still marks the parent unsuccessful if any item failed.
    Completed items survive recovery. With the default stop policy, output retains
    the completed prefix. With continue, output includes every input position and
    null for guarded or unsuccessful items. An empty list succeeds with []. Parent
    consumes zero attempts; each item has its own ledger.

    """

    items: str
    """Array reference without delimiters, such as input.invoices or steps.lookup.output.items."""
    action: WorkflowForEachActionSpec
    """Exactly one of run, path or outbound. No nested iteration, dependencies,
    waits, joins or exception routes. An optional when guard is evaluated once
    for each item using input.item, input.index and input.input, plus outputs of
    the parent's declared dependencies. Omitted input sends the item itself.
    Explicit input templates use the same item context. Inputs and guard decisions
    are snapshotted before dispatch and never reevaluated on retry. Mutating
    outbound retries require provider idempotency support.
    """
    max_parallel: int | Unset = UNSET
    """Maximum active items for this batch. Omit or set 0 for sequential execution."""
    on_item_failure: WorkflowForEachSpecOnItemFailure | Unset = UNSET
    """Continue after failed or dead items and mark the parent unsuccessful after all items are attempted. Omit to
    stop at the first failure."""

    def to_dict(self) -> dict[str, Any]:
        items = self.items

        action = self.action.to_dict()

        max_parallel = self.max_parallel

        on_item_failure: str | Unset = UNSET
        if not isinstance(self.on_item_failure, Unset):
            on_item_failure = self.on_item_failure

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "items": items,
                "action": action,
            }
        )
        if max_parallel is not UNSET:
            field_dict["max_parallel"] = max_parallel
        if on_item_failure is not UNSET:
            field_dict["on_item_failure"] = on_item_failure

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_for_each_action_spec import WorkflowForEachActionSpec

        d = dict(src_dict)
        items = d.pop("items")

        action = WorkflowForEachActionSpec.from_dict(d.pop("action"))

        max_parallel = d.pop("max_parallel", UNSET)

        _on_item_failure = d.pop("on_item_failure", UNSET)
        on_item_failure: WorkflowForEachSpecOnItemFailure | Unset
        if isinstance(_on_item_failure, Unset):
            on_item_failure = UNSET
        else:
            on_item_failure = check_workflow_for_each_spec_on_item_failure(_on_item_failure)

        workflow_for_each_spec = cls(
            items=items,
            action=action,
            max_parallel=max_parallel,
            on_item_failure=on_item_failure,
        )

        return workflow_for_each_spec
