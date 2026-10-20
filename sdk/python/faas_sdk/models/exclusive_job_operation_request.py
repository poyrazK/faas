from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.create_job_run_request import CreateJobRunRequest


T = TypeVar("T", bound="ExclusiveJobOperationRequest")


@_attrs_define
class ExclusiveJobOperationRequest:
    """Job run intent admitted under a named exclusive-operation policy. The account and Job identity are derived from the
    authenticated route.

    """

    policy: str
    key: bool | float | str
    """Business identifier for this Job run lane; account authorization scope is derived from the authenticated
    route."""
    run: CreateJobRunRequest
    """Atomic fan-out into indexed task records; supply `tasks`, an ordered
    `inputs` array, or an external `input_manifest_uri` and checksum.
    Each manifest entry is assigned to one task index in array order.
    The handler validates the count against `Plan.JobMaxTasksPerRun`
    (Hobby=100, Pro=1000, Scale=5000). Per-run overrides
    (parallelism / retry_max / task_timeout_sec) inherit from
    the job when null.
    """
    equivalence_key: str | Unset = UNSET
    """Optional identity for joining accepted Job runs only when their run intents are equivalent."""

    def to_dict(self) -> dict[str, Any]:
        policy = self.policy

        key: bool | float | str
        key = self.key

        run = self.run.to_dict()

        equivalence_key = self.equivalence_key

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "policy": policy,
                "key": key,
                "run": run,
            }
        )
        if equivalence_key is not UNSET:
            field_dict["equivalence_key"] = equivalence_key

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.create_job_run_request import CreateJobRunRequest

        d = dict(src_dict)
        policy = d.pop("policy")

        def _parse_key(data: object) -> bool | float | str:
            return cast(bool | float | str, data)

        key = _parse_key(d.pop("key"))

        run = CreateJobRunRequest.from_dict(d.pop("run"))

        equivalence_key = d.pop("equivalence_key", UNSET)

        exclusive_job_operation_request = cls(
            policy=policy,
            key=key,
            run=run,
            equivalence_key=equivalence_key,
        )

        return exclusive_job_operation_request
