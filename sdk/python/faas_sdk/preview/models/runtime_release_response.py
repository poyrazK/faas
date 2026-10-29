from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.runtime_release_response_architecture import (
    RuntimeReleaseResponseArchitecture,
    check_runtime_release_response_architecture,
)
from ..models.runtime_release_response_qualification import (
    RuntimeReleaseResponseQualification,
    check_runtime_release_response_qualification,
)
from ..models.runtime_release_response_runtime import (
    RuntimeReleaseResponseRuntime,
    check_runtime_release_response_runtime,
)

T = TypeVar("T", bound="RuntimeReleaseResponse")


@_attrs_define
class RuntimeReleaseResponse:
    """Exact published runtime base bytes; host kernel and function runner are separate components."""

    id: str
    runtime: RuntimeReleaseResponseRuntime
    architecture: RuntimeReleaseResponseArchitecture
    source_digest: str
    """Immutable OCI source manifest digest."""
    guest_init_digest: str
    """SHA-256 of the PID 1 binary injected into this base."""
    base_digest: str
    """SHA-256 of the exact published ext4 bytes."""
    layout_version: str
    published_at: datetime.datetime
    qualification: RuntimeReleaseResponseQualification
    """Publication alone establishes no upgrade compatibility verdict."""

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        runtime: str = self.runtime

        architecture: str = self.architecture

        source_digest = self.source_digest

        guest_init_digest = self.guest_init_digest

        base_digest = self.base_digest

        layout_version = self.layout_version

        published_at = self.published_at.isoformat()

        qualification: str = self.qualification

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "runtime": runtime,
                "architecture": architecture,
                "source_digest": source_digest,
                "guest_init_digest": guest_init_digest,
                "base_digest": base_digest,
                "layout_version": layout_version,
                "published_at": published_at,
                "qualification": qualification,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        runtime = check_runtime_release_response_runtime(d.pop("runtime"))

        architecture = check_runtime_release_response_architecture(d.pop("architecture"))

        source_digest = d.pop("source_digest")

        guest_init_digest = d.pop("guest_init_digest")

        base_digest = d.pop("base_digest")

        layout_version = d.pop("layout_version")

        published_at = datetime.datetime.fromisoformat(d.pop("published_at"))

        qualification = check_runtime_release_response_qualification(d.pop("qualification"))

        runtime_release_response = cls(
            id=id,
            runtime=runtime,
            architecture=architecture,
            source_digest=source_digest,
            guest_init_digest=guest_init_digest,
            base_digest=base_digest,
            layout_version=layout_version,
            published_at=published_at,
            qualification=qualification,
        )

        return runtime_release_response
