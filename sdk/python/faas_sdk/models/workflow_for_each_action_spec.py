from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.workflow_for_each_action_spec_method import (
    WorkflowForEachActionSpecMethod,
    check_workflow_for_each_action_spec_method,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_for_each_action_spec_input_type_0 import WorkflowForEachActionSpecInputType0
    from ..models.workflow_guard_spec import WorkflowGuardSpec
    from ..models.workflow_outbound_spec import WorkflowOutboundSpec
    from ..models.workflow_retry_spec import WorkflowRetrySpec


T = TypeVar("T", bound="WorkflowForEachActionSpec")


@_attrs_define
class WorkflowForEachActionSpec:
    """Exactly one of run, path or outbound. No nested iteration, dependencies,
    waits, joins or exception routes. An optional when guard is evaluated once
    for each item using input.item, input.index and input.input, plus outputs of
    the parent's declared dependencies. Omitted input sends the item itself.
    Explicit input templates use the same item context. Inputs and guard decisions
    are snapshotted before dispatch and never reevaluated on retry. Mutating
    outbound retries require provider idempotency support.

    """

    run: str | Unset = UNSET
    path: str | Unset = UNSET
    outbound: WorkflowOutboundSpec | Unset = UNSET
    """Call an existing customer managed outbound integration bound to this app.
    Credentials and fixed-origin routing remain with outboundd. Input is a
    templated JSON body. Path segments and query values support the workflow
    template syntax; dynamic path values are escaped as one segment. GET and
    HEAD have no body and forbid explicit input.
    One provider call occurs per workflow attempt. Automatic mutating retries
    require explicit provider idempotency support. Workflow outputs contain
    status and body; sensitive headers and failed-response bodies are omitted.
    """
    method: WorkflowForEachActionSpecMethod | Unset = UNSET
    input_: bool | float | list[Any] | None | str | Unset | WorkflowForEachActionSpecInputType0 = UNSET
    """Typed JSON input template for each item."""
    timeout: str | Unset = UNSET
    """Per-item action timeout, bounded by the workflow plan limit."""
    retry: WorkflowRetrySpec | Unset = UNSET
    """Retry policy for one workflow step."""
    when: WorkflowGuardSpec | Unset = UNSET
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

    def to_dict(self) -> dict[str, Any]:
        from ..models.workflow_for_each_action_spec_input_type_0 import WorkflowForEachActionSpecInputType0

        run = self.run

        path = self.path

        outbound: dict[str, Any] | Unset = UNSET
        if not isinstance(self.outbound, Unset):
            outbound = self.outbound.to_dict()

        method: str | Unset = UNSET
        if not isinstance(self.method, Unset):
            method = self.method

        input_: bool | dict[str, Any] | float | list[Any] | None | str | Unset
        if isinstance(self.input_, Unset):
            input_ = UNSET
        elif isinstance(self.input_, WorkflowForEachActionSpecInputType0):
            input_ = self.input_.to_dict()
        elif isinstance(self.input_, list):
            input_ = self.input_

        else:
            input_ = self.input_

        timeout = self.timeout

        retry: dict[str, Any] | Unset = UNSET
        if not isinstance(self.retry, Unset):
            retry = self.retry.to_dict()

        when: dict[str, Any] | Unset = UNSET
        if not isinstance(self.when, Unset):
            when = self.when.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if run is not UNSET:
            field_dict["run"] = run
        if path is not UNSET:
            field_dict["path"] = path
        if outbound is not UNSET:
            field_dict["outbound"] = outbound
        if method is not UNSET:
            field_dict["method"] = method
        if input_ is not UNSET:
            field_dict["input"] = input_
        if timeout is not UNSET:
            field_dict["timeout"] = timeout
        if retry is not UNSET:
            field_dict["retry"] = retry
        if when is not UNSET:
            field_dict["when"] = when

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_for_each_action_spec_input_type_0 import WorkflowForEachActionSpecInputType0
        from ..models.workflow_guard_spec import WorkflowGuardSpec
        from ..models.workflow_outbound_spec import WorkflowOutboundSpec
        from ..models.workflow_retry_spec import WorkflowRetrySpec

        d = dict(src_dict)
        run = d.pop("run", UNSET)

        path = d.pop("path", UNSET)

        _outbound = d.pop("outbound", UNSET)
        outbound: WorkflowOutboundSpec | Unset
        if isinstance(_outbound, Unset):
            outbound = UNSET
        else:
            outbound = WorkflowOutboundSpec.from_dict(_outbound)

        _method = d.pop("method", UNSET)
        method: WorkflowForEachActionSpecMethod | Unset
        if isinstance(_method, Unset):
            method = UNSET
        else:
            method = check_workflow_for_each_action_spec_method(_method)

        def _parse_input_(
            data: object,
        ) -> bool | float | list[Any] | None | str | Unset | WorkflowForEachActionSpecInputType0:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                input_type_0 = WorkflowForEachActionSpecInputType0.from_dict(data)

                return input_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, list):
                    raise TypeError()
                input_type_1 = cast(list[Any], data)

                return input_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(bool | float | list[Any] | None | str | Unset | WorkflowForEachActionSpecInputType0, data)

        input_ = _parse_input_(d.pop("input", UNSET))

        timeout = d.pop("timeout", UNSET)

        _retry = d.pop("retry", UNSET)
        retry: WorkflowRetrySpec | Unset
        if isinstance(_retry, Unset):
            retry = UNSET
        else:
            retry = WorkflowRetrySpec.from_dict(_retry)

        _when = d.pop("when", UNSET)
        when: WorkflowGuardSpec | Unset
        if isinstance(_when, Unset):
            when = UNSET
        else:
            when = WorkflowGuardSpec.from_dict(_when)

        workflow_for_each_action_spec = cls(
            run=run,
            path=path,
            outbound=outbound,
            method=method,
            input_=input_,
            timeout=timeout,
            retry=retry,
            when=when,
        )

        return workflow_for_each_action_spec
