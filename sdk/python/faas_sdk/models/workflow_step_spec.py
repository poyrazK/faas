from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_step_spec_method import WorkflowStepSpecMethod, check_workflow_step_spec_method
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_condition_spec import WorkflowConditionSpec
    from ..models.workflow_for_each_spec import WorkflowForEachSpec
    from ..models.workflow_guard_spec import WorkflowGuardSpec
    from ..models.workflow_join_spec import WorkflowJoinSpec
    from ..models.workflow_outbound_spec import WorkflowOutboundSpec
    from ..models.workflow_retry_spec import WorkflowRetrySpec
    from ..models.workflow_step_spec_input_type_0 import WorkflowStepSpecInputType0


T = TypeVar("T", bound="WorkflowStepSpec")


@_attrs_define
class WorkflowStepSpec:
    """One workflow step. The canonical ADR-081 target is `run`; `path`
    and `method` remain accepted for the existing HTTP wake executor
    during the runtime migration. Exactly one of `run`, `path`,
    `wait_for_event`, `wait_for_callback`, `wait_for_duration`,
    `wait_for_condition`, `outbound`, `join`, or `for_each` must be supplied.
    Set `managed_operation` on an executable HTTP step to persist its business
    result transactionally and replay it safely when the workflow retries
    after an uncertain response.

    """

    name: str
    run: str | Unset = UNSET
    """Named platform operation to invoke."""
    managed_operation: bool | Unset = UNSET
    """Opt into the managed PostgreSQL operation result protocol for this executable HTTP step. The handler must
    use the transactional operation SDK."""
    for_each: WorkflowForEachSpec | Unset = UNSET
    """Sequential action over a JSON array from input or a direct dependency output.
    Snapshots all items and resolved action inputs before dispatch. At most 128
    items, 1 MiB source/prepared inputs and 1 MiB collected output. Parent names
    permit at most 64 UTF-8 bytes. Stops on item failure; completed items survive
    recovery. Output is an array of item outputs in input order; an empty list
    succeeds with []. Parent consumes zero attempts; each item has its own ledger.
    """
    join: WorkflowJoinSpec | Unset = UNSET
    """Native branch join. Waits for all dependencies to finish and permits only
    skips caused by false guards, including their descendants. Failed,
    cancelled, unknown and exception-route skips cannot activate a join.
    The first succeeded dependency in output_from order supplies the durable
    output {source: step name, value: original output}. All inactive branches
    skip the join and its continuation. Consumes zero execution attempts.
    Requires 2-128 dependencies and cannot have input, method, when, timeout,
    retry or exception routes, or be/depend on an exception handler.
    """
    outbound: WorkflowOutboundSpec | Unset = UNSET
    """Call an existing customer managed outbound integration bound to this app.
    Credentials and fixed-origin routing remain with outboundd. Input is a
    templated JSON body; GET and HEAD have no body and forbid explicit input.
    One provider call occurs per workflow attempt. Automatic mutating retries
    require explicit provider idempotency support. Workflow outputs contain
    status and body; sensitive headers and failed-response bodies are omitted.
    """
    input_: bool | float | list[Any] | None | str | Unset | WorkflowStepSpecInputType0 = UNSET
    """JSON input passed to the named operation."""
    path: str | Unset = UNSET
    """HTTP wake path, retained for compatibility with the existing executor."""
    method: WorkflowStepSpecMethod | Unset = UNSET
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
    depends_on: list[str] | Unset = UNSET
    wait_for_event: str | Unset = UNSET
    wait_for_callback: bool | Unset = UNSET
    """Park for one account-authorized callback completion. Requires a wait timeout."""
    wait_for_duration: str | Unset = UNSET
    """Durable timer, from 1s up to the plan's 7-day workflow wait limit. Fixed day suffixes such as `3d` mean
    24-hour days; no compute is held while waiting."""
    wait_for_condition: WorkflowConditionSpec | Unset = UNSET
    """Bounded scheduled checker. Each 2xx response must be a JSON object with boolean done. A false response
    becomes the next check's input; no compute is held between checks."""
    timeout: str | Unset = UNSET
    """Step or wait timeout in time.ParseDuration form, for example `30s`; workflow also accepts fixed 24-hour day
    suffixes such as `7d`."""
    on_timeout: str | Unset = UNSET
    on_failure: str | Unset = UNSET
    """Name of the handler step to run after this step reaches a terminal failure."""
    retry: None | Unset | WorkflowRetrySpec = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.workflow_retry_spec import WorkflowRetrySpec
        from ..models.workflow_step_spec_input_type_0 import WorkflowStepSpecInputType0

        name = self.name

        run = self.run

        managed_operation = self.managed_operation

        for_each: dict[str, Any] | Unset = UNSET
        if not isinstance(self.for_each, Unset):
            for_each = self.for_each.to_dict()

        join: dict[str, Any] | Unset = UNSET
        if not isinstance(self.join, Unset):
            join = self.join.to_dict()

        outbound: dict[str, Any] | Unset = UNSET
        if not isinstance(self.outbound, Unset):
            outbound = self.outbound.to_dict()

        input_: bool | dict[str, Any] | float | list[Any] | None | str | Unset
        if isinstance(self.input_, Unset):
            input_ = UNSET
        elif isinstance(self.input_, WorkflowStepSpecInputType0):
            input_ = self.input_.to_dict()
        elif isinstance(self.input_, list):
            input_ = self.input_

        else:
            input_ = self.input_

        path = self.path

        method: str | Unset = UNSET
        if not isinstance(self.method, Unset):
            method = self.method

        when: dict[str, Any] | Unset = UNSET
        if not isinstance(self.when, Unset):
            when = self.when.to_dict()

        depends_on: list[str] | Unset = UNSET
        if not isinstance(self.depends_on, Unset):
            depends_on = self.depends_on

        wait_for_event = self.wait_for_event

        wait_for_callback = self.wait_for_callback

        wait_for_duration = self.wait_for_duration

        wait_for_condition: dict[str, Any] | Unset = UNSET
        if not isinstance(self.wait_for_condition, Unset):
            wait_for_condition = self.wait_for_condition.to_dict()

        timeout = self.timeout

        on_timeout = self.on_timeout

        on_failure = self.on_failure

        retry: dict[str, Any] | None | Unset
        if isinstance(self.retry, Unset):
            retry = UNSET
        elif isinstance(self.retry, WorkflowRetrySpec):
            retry = self.retry.to_dict()
        else:
            retry = self.retry

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
            }
        )
        if run is not UNSET:
            field_dict["run"] = run
        if managed_operation is not UNSET:
            field_dict["managed_operation"] = managed_operation
        if for_each is not UNSET:
            field_dict["for_each"] = for_each
        if join is not UNSET:
            field_dict["join"] = join
        if outbound is not UNSET:
            field_dict["outbound"] = outbound
        if input_ is not UNSET:
            field_dict["input"] = input_
        if path is not UNSET:
            field_dict["path"] = path
        if method is not UNSET:
            field_dict["method"] = method
        if when is not UNSET:
            field_dict["when"] = when
        if depends_on is not UNSET:
            field_dict["depends_on"] = depends_on
        if wait_for_event is not UNSET:
            field_dict["wait_for_event"] = wait_for_event
        if wait_for_callback is not UNSET:
            field_dict["wait_for_callback"] = wait_for_callback
        if wait_for_duration is not UNSET:
            field_dict["wait_for_duration"] = wait_for_duration
        if wait_for_condition is not UNSET:
            field_dict["wait_for_condition"] = wait_for_condition
        if timeout is not UNSET:
            field_dict["timeout"] = timeout
        if on_timeout is not UNSET:
            field_dict["on_timeout"] = on_timeout
        if on_failure is not UNSET:
            field_dict["on_failure"] = on_failure
        if retry is not UNSET:
            field_dict["retry"] = retry

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_condition_spec import WorkflowConditionSpec
        from ..models.workflow_for_each_spec import WorkflowForEachSpec
        from ..models.workflow_guard_spec import WorkflowGuardSpec
        from ..models.workflow_join_spec import WorkflowJoinSpec
        from ..models.workflow_outbound_spec import WorkflowOutboundSpec
        from ..models.workflow_retry_spec import WorkflowRetrySpec
        from ..models.workflow_step_spec_input_type_0 import WorkflowStepSpecInputType0

        d = dict(src_dict)
        name = d.pop("name")

        run = d.pop("run", UNSET)

        managed_operation = d.pop("managed_operation", UNSET)

        _for_each = d.pop("for_each", UNSET)
        for_each: WorkflowForEachSpec | Unset
        if isinstance(_for_each, Unset):
            for_each = UNSET
        else:
            for_each = WorkflowForEachSpec.from_dict(_for_each)

        _join = d.pop("join", UNSET)
        join: WorkflowJoinSpec | Unset
        if isinstance(_join, Unset):
            join = UNSET
        else:
            join = WorkflowJoinSpec.from_dict(_join)

        _outbound = d.pop("outbound", UNSET)
        outbound: WorkflowOutboundSpec | Unset
        if isinstance(_outbound, Unset):
            outbound = UNSET
        else:
            outbound = WorkflowOutboundSpec.from_dict(_outbound)

        def _parse_input_(data: object) -> bool | float | list[Any] | None | str | Unset | WorkflowStepSpecInputType0:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                input_type_0 = WorkflowStepSpecInputType0.from_dict(data)

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
            return cast(bool | float | list[Any] | None | str | Unset | WorkflowStepSpecInputType0, data)

        input_ = _parse_input_(d.pop("input", UNSET))

        path = d.pop("path", UNSET)

        _method = d.pop("method", UNSET)
        method: WorkflowStepSpecMethod | Unset
        if isinstance(_method, Unset):
            method = UNSET
        else:
            method = check_workflow_step_spec_method(_method)

        _when = d.pop("when", UNSET)
        when: WorkflowGuardSpec | Unset
        if isinstance(_when, Unset):
            when = UNSET
        else:
            when = WorkflowGuardSpec.from_dict(_when)

        depends_on = cast(list[str], d.pop("depends_on", UNSET))

        wait_for_event = d.pop("wait_for_event", UNSET)

        wait_for_callback = d.pop("wait_for_callback", UNSET)

        wait_for_duration = d.pop("wait_for_duration", UNSET)

        _wait_for_condition = d.pop("wait_for_condition", UNSET)
        wait_for_condition: WorkflowConditionSpec | Unset
        if isinstance(_wait_for_condition, Unset):
            wait_for_condition = UNSET
        else:
            wait_for_condition = WorkflowConditionSpec.from_dict(_wait_for_condition)

        timeout = d.pop("timeout", UNSET)

        on_timeout = d.pop("on_timeout", UNSET)

        on_failure = d.pop("on_failure", UNSET)

        def _parse_retry(data: object) -> None | Unset | WorkflowRetrySpec:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                retry_type_0 = WorkflowRetrySpec.from_dict(data)

                return retry_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | Unset | WorkflowRetrySpec, data)

        retry = _parse_retry(d.pop("retry", UNSET))

        workflow_step_spec = cls(
            name=name,
            run=run,
            managed_operation=managed_operation,
            for_each=for_each,
            join=join,
            outbound=outbound,
            input_=input_,
            path=path,
            method=method,
            when=when,
            depends_on=depends_on,
            wait_for_event=wait_for_event,
            wait_for_callback=wait_for_callback,
            wait_for_duration=wait_for_duration,
            wait_for_condition=wait_for_condition,
            timeout=timeout,
            on_timeout=on_timeout,
            on_failure=on_failure,
            retry=retry,
        )

        workflow_step_spec.additional_properties = d
        return workflow_step_spec

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
