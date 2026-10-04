from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.workflow_for_each_action_spec import WorkflowForEachActionSpec


T = TypeVar("T", bound="WorkflowForEachSpec")


@_attrs_define
class WorkflowForEachSpec:
    """Sequential action over a JSON array from input or a direct dependency output.
    Snapshots all items and resolved action inputs before dispatch. At most 128
    items, 1 MiB source/prepared inputs and 1 MiB collected output. Parent names
    permit at most 64 UTF-8 bytes. Stops on item failure; completed items survive
    recovery. Output is an array of item outputs in input order; an empty list
    succeeds with []. Parent consumes zero attempts; each item has its own ledger.

    """

    items: str
    """Array reference without delimiters, such as input.invoices or steps.lookup.output.items."""
    action: WorkflowForEachActionSpec
    """Exactly one of run, path or outbound. No nested iteration, dependencies,
    guards or exception routes. Omitted input sends the item itself. Explicit
    input templates read input.item, input.index, input.input (original run input)
    and outputs of the parent's declared dependencies. Inputs are never re-rendered
    on retry. Mutating outbound retries require provider idempotency support.
    """

    def to_dict(self) -> dict[str, Any]:
        items = self.items

        action = self.action.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "items": items,
                "action": action,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_for_each_action_spec import WorkflowForEachActionSpec

        d = dict(src_dict)
        items = d.pop("items")

        action = WorkflowForEachActionSpec.from_dict(d.pop("action"))

        workflow_for_each_spec = cls(
            items=items,
            action=action,
        )

        return workflow_for_each_spec
