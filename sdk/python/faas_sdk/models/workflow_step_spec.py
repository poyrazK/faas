from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_step_spec_method import WorkflowStepSpecMethod, check_workflow_step_spec_method
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_retry_spec import WorkflowRetrySpec
    from ..models.workflow_step_spec_input_type_0 import WorkflowStepSpecInputType0


T = TypeVar("T", bound="WorkflowStepSpec")


@_attrs_define
class WorkflowStepSpec:
    """One workflow step. The canonical ADR-081 target is `run`; `path`
    and `method` remain accepted for the existing HTTP wake executor
    during the runtime migration. Exactly one of `run`, `path`, or
    `wait_for_event` must be supplied.

    """

    name: str
    run: str | Unset = UNSET
    """Named platform operation to invoke."""
    input_: bool | float | list[Any] | None | str | Unset | WorkflowStepSpecInputType0 = UNSET
    """JSON input passed to the named operation."""
    path: str | Unset = UNSET
    """HTTP wake path, retained for compatibility with the existing executor."""
    method: WorkflowStepSpecMethod | Unset = UNSET
    depends_on: list[str] | Unset = UNSET
    wait_for_event: str | Unset = UNSET
    timeout: str | Unset = UNSET
    """Step or wait timeout in time.ParseDuration form, for example `30s`; workflow also accepts fixed 24-hour day
    suffixes such as `7d`."""
    on_timeout: str | Unset = UNSET
    retry: None | Unset | WorkflowRetrySpec = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.workflow_retry_spec import WorkflowRetrySpec
        from ..models.workflow_step_spec_input_type_0 import WorkflowStepSpecInputType0

        name = self.name

        run = self.run

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

        depends_on: list[str] | Unset = UNSET
        if not isinstance(self.depends_on, Unset):
            depends_on = self.depends_on

        wait_for_event = self.wait_for_event

        timeout = self.timeout

        on_timeout = self.on_timeout

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
        if input_ is not UNSET:
            field_dict["input"] = input_
        if path is not UNSET:
            field_dict["path"] = path
        if method is not UNSET:
            field_dict["method"] = method
        if depends_on is not UNSET:
            field_dict["depends_on"] = depends_on
        if wait_for_event is not UNSET:
            field_dict["wait_for_event"] = wait_for_event
        if timeout is not UNSET:
            field_dict["timeout"] = timeout
        if on_timeout is not UNSET:
            field_dict["on_timeout"] = on_timeout
        if retry is not UNSET:
            field_dict["retry"] = retry

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_retry_spec import WorkflowRetrySpec
        from ..models.workflow_step_spec_input_type_0 import WorkflowStepSpecInputType0

        d = dict(src_dict)
        name = d.pop("name")

        run = d.pop("run", UNSET)

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

        depends_on = cast(list[str], d.pop("depends_on", UNSET))

        wait_for_event = d.pop("wait_for_event", UNSET)

        timeout = d.pop("timeout", UNSET)

        on_timeout = d.pop("on_timeout", UNSET)

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
            input_=input_,
            path=path,
            method=method,
            depends_on=depends_on,
            wait_for_event=wait_for_event,
            timeout=timeout,
            on_timeout=on_timeout,
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
