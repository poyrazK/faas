from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.build_response_cache_status import BuildResponseCacheStatus, check_build_response_cache_status
from ..models.build_response_failure_class import BuildResponseFailureClass, check_build_response_failure_class
from ..models.build_response_kind import BuildResponseKind, check_build_response_kind
from ..models.build_response_status import BuildResponseStatus, check_build_response_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="BuildResponse")


@_attrs_define
class BuildResponse:
    """DEPLOY-PROV-6 / ADR-089 (issue #741): the LIFECYCLE row for
    a single build — current status, enqueued/started/finished
    timestamps, failure_class, server-computed duration_seconds.
    Companion to BuildProvenanceResponse (post-mortem export,
    ADR-038) and the /sbom route (post-mortem blob, ADR-038
    Phase 3). The status field mirrors builds.status — a
    4-state enum `queued|running|succeeded|failed` per the
    `builds_status_check` CHECK constraint. 'cancelled' is
    intentionally absent (ADR-089 §1).

    failure_class is the low-cardinality enum
    `oom|timeout|user_error|infra` per the
    `builds_failure_class_check` CHECK; present only when
    status='failed'.

    duration_seconds is server-computed (FinishedAt − StartedAt)
    only when BOTH timestamps are populated; absent otherwise
    (so a queued/running build stays minimal). CI scripts can
    rely on its presence as "the build reached a terminal state
    and elapsed N wall-clock seconds." error_message is
    intentionally NOT in this response — it lives on
    deployments; clients that need the per-failure string call
    GET /v1/deployments/{id}.

    """

    id: str
    deployment_id: str
    kind: BuildResponseKind
    source_bytes: int
    status: BuildResponseStatus
    enqueued_at: datetime.datetime
    failure_class: BuildResponseFailureClass | Unset = UNSET
    log_path: str | Unset = UNSET
    started_at: datetime.datetime | Unset = UNSET
    finished_at: datetime.datetime | Unset = UNSET
    duration_seconds: int | Unset = UNSET
    """Server-computed FinishedAt − StartedAt in whole seconds. Absent until the build reaches a terminal state."""
    cache_status: BuildResponseCacheStatus | Unset = UNSET
    """Builderd cache decision for this build."""
    cache_key_sha256: str | Unset = UNSET
    """SHA-256 digest of the versioned BuildCacheRecipe."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        deployment_id = self.deployment_id

        kind: str = self.kind

        source_bytes = self.source_bytes

        status: str = self.status

        enqueued_at = self.enqueued_at.isoformat()

        failure_class: str | Unset = UNSET
        if not isinstance(self.failure_class, Unset):
            failure_class = self.failure_class

        log_path = self.log_path

        started_at: str | Unset = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        finished_at: str | Unset = UNSET
        if not isinstance(self.finished_at, Unset):
            finished_at = self.finished_at.isoformat()

        duration_seconds = self.duration_seconds

        cache_status: str | Unset = UNSET
        if not isinstance(self.cache_status, Unset):
            cache_status = self.cache_status

        cache_key_sha256 = self.cache_key_sha256

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "deployment_id": deployment_id,
                "kind": kind,
                "source_bytes": source_bytes,
                "status": status,
                "enqueued_at": enqueued_at,
            }
        )
        if failure_class is not UNSET:
            field_dict["failure_class"] = failure_class
        if log_path is not UNSET:
            field_dict["log_path"] = log_path
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at
        if duration_seconds is not UNSET:
            field_dict["duration_seconds"] = duration_seconds
        if cache_status is not UNSET:
            field_dict["cache_status"] = cache_status
        if cache_key_sha256 is not UNSET:
            field_dict["cache_key_sha256"] = cache_key_sha256

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        deployment_id = d.pop("deployment_id")

        kind = check_build_response_kind(d.pop("kind"))

        source_bytes = d.pop("source_bytes")

        status = check_build_response_status(d.pop("status"))

        enqueued_at = datetime.datetime.fromisoformat(d.pop("enqueued_at"))

        _failure_class = d.pop("failure_class", UNSET)
        failure_class: BuildResponseFailureClass | Unset
        if isinstance(_failure_class, Unset):
            failure_class = UNSET
        else:
            failure_class = check_build_response_failure_class(_failure_class)

        log_path = d.pop("log_path", UNSET)

        _started_at = d.pop("started_at", UNSET)
        started_at: datetime.datetime | Unset
        if isinstance(_started_at, Unset):
            started_at = UNSET
        else:
            started_at = datetime.datetime.fromisoformat(_started_at)

        _finished_at = d.pop("finished_at", UNSET)
        finished_at: datetime.datetime | Unset
        if isinstance(_finished_at, Unset):
            finished_at = UNSET
        else:
            finished_at = datetime.datetime.fromisoformat(_finished_at)

        duration_seconds = d.pop("duration_seconds", UNSET)

        _cache_status = d.pop("cache_status", UNSET)
        cache_status: BuildResponseCacheStatus | Unset
        if isinstance(_cache_status, Unset):
            cache_status = UNSET
        else:
            cache_status = check_build_response_cache_status(_cache_status)

        cache_key_sha256 = d.pop("cache_key_sha256", UNSET)

        build_response = cls(
            id=id,
            deployment_id=deployment_id,
            kind=kind,
            source_bytes=source_bytes,
            status=status,
            enqueued_at=enqueued_at,
            failure_class=failure_class,
            log_path=log_path,
            started_at=started_at,
            finished_at=finished_at,
            duration_seconds=duration_seconds,
            cache_status=cache_status,
            cache_key_sha256=cache_key_sha256,
        )

        build_response.additional_properties = d
        return build_response

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
