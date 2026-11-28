from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_retry_spec import WorkflowRetrySpec


T = TypeVar("T", bound="WorkflowCodeSpec")


@_attrs_define
class WorkflowCodeSpec:
    """A code-first workflow's entry handler. Exactly one of run or path. Gregale POSTs the run's journal to it at every
    durable boundary; the SDKs (@gregale/sdk-node/workflows, faas_sdk.workflows) answer with the operations the code
    reaches next. A code workflow has an empty steps list.

    """

    run: str | Unset = UNSET
    """Handler name; the entry path is /<run>."""
    path: str | Unset = UNSET
    """Explicit entry path starting with '/'."""
    timeout: str | Unset = UNSET
    """Per-call timeout as a duration string; at most the plan's step timeout. Default 30s."""
    retry: WorkflowRetrySpec | Unset = UNSET
    """Retry policy for one workflow step."""

    def to_dict(self) -> dict[str, Any]:
        run = self.run

        path = self.path

        timeout = self.timeout

        retry: dict[str, Any] | Unset = UNSET
        if not isinstance(self.retry, Unset):
            retry = self.retry.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if run is not UNSET:
            field_dict["run"] = run
        if path is not UNSET:
            field_dict["path"] = path
        if timeout is not UNSET:
            field_dict["timeout"] = timeout
        if retry is not UNSET:
            field_dict["retry"] = retry

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_retry_spec import WorkflowRetrySpec

        d = dict(src_dict)
        run = d.pop("run", UNSET)

        path = d.pop("path", UNSET)

        timeout = d.pop("timeout", UNSET)

        _retry = d.pop("retry", UNSET)
        retry: WorkflowRetrySpec | Unset
        if isinstance(_retry, Unset):
            retry = UNSET
        else:
            retry = WorkflowRetrySpec.from_dict(_retry)

        workflow_code_spec = cls(
            run=run,
            path=path,
            timeout=timeout,
            retry=retry,
        )

        return workflow_code_spec
