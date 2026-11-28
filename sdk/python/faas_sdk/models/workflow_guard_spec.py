from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.workflow_guard_spec_op import WorkflowGuardSpecOp, check_workflow_guard_spec_op
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowGuardSpec")


@_attrs_define
class WorkflowGuardSpec:
    """Bounded declarative when predicate. Specify exactly one of all, any,
    not, or ref/op/value. References select input or a direct dependency
    output using input.foo or steps.lookup.output.body.foo without template
    delimiters. Equality is type-sensitive and accepts scalar literals only.
    Numeric comparisons are exact; numbers are bounded to 4096 bytes and
    exponent magnitude 4096. Missing paths fail comparisons, including ne;
    exists distinguishes missing from present null. not negates normally.
    At most 32 predicate nodes, 8 levels and 16 KiB per guard. Guards are
    forbidden on on_failure/on_timeout handler targets. A false guard skips
    its step and propagates through dependencies; skipped paths do not join.

    """

    all_: list[WorkflowGuardSpec] | Unset = UNSET
    any_: list[WorkflowGuardSpec] | Unset = UNSET
    not_: WorkflowGuardSpec | Unset = UNSET
    """Bounded declarative when predicate. Specify exactly one of all, any,
    not, or ref/op/value. References select input or a direct dependency
    output using input.foo or steps.lookup.output.body.foo without template
    delimiters. Equality is type-sensitive and accepts scalar literals only.
    Numeric comparisons are exact; numbers are bounded to 4096 bytes and
    exponent magnitude 4096. Missing paths fail comparisons, including ne;
    exists distinguishes missing from present null. not negates normally.
    At most 32 predicate nodes, 8 levels and 16 KiB per guard. Guards are
    forbidden on on_failure/on_timeout handler targets. A false guard skips
    its step and propagates through dependencies; skipped paths do not join.
    """
    ref: str | Unset = UNSET
    """Input path or output path of a declared direct dependency."""
    op: WorkflowGuardSpecOp | Unset = UNSET
    value: bool | float | None | str | Unset = UNSET
    """Scalar JSON literal; numeric operators require a number and exists requires a boolean."""

    def to_dict(self) -> dict[str, Any]:
        all_: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.all_, Unset):
            all_ = []
            for all_item_data in self.all_:
                all_item = all_item_data.to_dict()
                all_.append(all_item)

        any_: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.any_, Unset):
            any_ = []
            for any_item_data in self.any_:
                any_item = any_item_data.to_dict()
                any_.append(any_item)

        not_: dict[str, Any] | Unset = UNSET
        if not isinstance(self.not_, Unset):
            not_ = self.not_.to_dict()

        ref = self.ref

        op: str | Unset = UNSET
        if not isinstance(self.op, Unset):
            op = self.op

        value: bool | float | None | str | Unset
        if isinstance(self.value, Unset):
            value = UNSET
        else:
            value = self.value

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if all_ is not UNSET:
            field_dict["all"] = all_
        if any_ is not UNSET:
            field_dict["any"] = any_
        if not_ is not UNSET:
            field_dict["not"] = not_
        if ref is not UNSET:
            field_dict["ref"] = ref
        if op is not UNSET:
            field_dict["op"] = op
        if value is not UNSET:
            field_dict["value"] = value

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _all_ = d.pop("all", UNSET)
        all_: list[WorkflowGuardSpec] | Unset = UNSET
        if _all_ is not UNSET:
            all_ = []
            for all_item_data in _all_:
                all_item = WorkflowGuardSpec.from_dict(all_item_data)

                all_.append(all_item)

        _any_ = d.pop("any", UNSET)
        any_: list[WorkflowGuardSpec] | Unset = UNSET
        if _any_ is not UNSET:
            any_ = []
            for any_item_data in _any_:
                any_item = WorkflowGuardSpec.from_dict(any_item_data)

                any_.append(any_item)

        _not_ = d.pop("not", UNSET)
        not_: WorkflowGuardSpec | Unset
        if isinstance(_not_, Unset):
            not_ = UNSET
        else:
            not_ = WorkflowGuardSpec.from_dict(_not_)

        ref = d.pop("ref", UNSET)

        _op = d.pop("op", UNSET)
        op: WorkflowGuardSpecOp | Unset
        if isinstance(_op, Unset):
            op = UNSET
        else:
            op = check_workflow_guard_spec_op(_op)

        def _parse_value(data: object) -> bool | float | None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(bool | float | None | str | Unset, data)

        value = _parse_value(d.pop("value", UNSET))

        workflow_guard_spec = cls(
            all_=all_,
            any_=any_,
            not_=not_,
            ref=ref,
            op=op,
            value=value,
        )

        return workflow_guard_spec
