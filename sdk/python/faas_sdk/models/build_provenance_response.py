from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="BuildProvenanceResponse")


@_attrs_define
class BuildProvenanceResponse:
    """ADR-038 / Tier 3 / issue #197 B3.10-read half: the
    post-mortem "what ran?" record for a single successful build.
    Field names mirror the `build_provenance` table columns. Empty
    strings indicate a column the populator hasn't filled yet —
    `buildkit_version`, `railpack_version`, `base_digest`,
    `runner_digest`, and `sbom_storage_key` are populated by
    Phase 3 (cosign signer + syft SBOM), but the columns exist
    today so Phase 3 is a zero-cost schema change.
    `framework_version` is the language version the customer's
    source declares (`.nvmrc`, `package.json::engines.node`,
    `pyproject.toml::requires-python`, `.python-version`,
    `.tool-versions`, `go.mod` directive); added in issue #740 /
    DEPLOY-PROV-5 / ADR-087. OBSERVATIONAL ONLY — the build
    pipeline never reads it (the runtime is bound by the OCI
    base ref via `FAAS_DEPLOY_BASE_REF_<RUNTIME>`).

    """

    id: UUID
    build_id: str
    source_sha256: str
    """sha256 of the customer's source tarball (the cache lookup key)."""
    plan: str
    """free / hobby / pro / scale — copied from the account at claim time."""
    builder_node_id: str
    """compute_node name (default `default-local` on the one-box)."""
    started_at: datetime.datetime
    finished_at: datetime.datetime
    buildkit_version: str | Unset = UNSET
    railpack_version: str | Unset = UNSET
    base_digest: str | Unset = UNSET
    source_url: str | Unset = UNSET
    commit_sha: str | Unset = UNSET
    runner_digest: str | Unset = UNSET
    sbom_storage_key: None | str | Unset = UNSET
    """Phase 3 populator fills this from `syft` output. Empty string when not yet populated."""
    framework_version: None | str | Unset = UNSET
    """Source-declared language version (nodes 22.11.0 / python 3.13 / go 1.24, etc.). Empty string when no version
    file is present or any parser fails — best-effort, never an error. Added in DEPLOY-PROV-5 / issue #740 /
    ADR-087."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        build_id = self.build_id

        source_sha256 = self.source_sha256

        plan = self.plan

        builder_node_id = self.builder_node_id

        started_at = self.started_at.isoformat()

        finished_at = self.finished_at.isoformat()

        buildkit_version = self.buildkit_version

        railpack_version = self.railpack_version

        base_digest = self.base_digest

        source_url = self.source_url

        commit_sha = self.commit_sha

        runner_digest = self.runner_digest

        sbom_storage_key: None | str | Unset
        if isinstance(self.sbom_storage_key, Unset):
            sbom_storage_key = UNSET
        else:
            sbom_storage_key = self.sbom_storage_key

        framework_version: None | str | Unset
        if isinstance(self.framework_version, Unset):
            framework_version = UNSET
        else:
            framework_version = self.framework_version

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "build_id": build_id,
                "source_sha256": source_sha256,
                "plan": plan,
                "builder_node_id": builder_node_id,
                "started_at": started_at,
                "finished_at": finished_at,
            }
        )
        if buildkit_version is not UNSET:
            field_dict["buildkit_version"] = buildkit_version
        if railpack_version is not UNSET:
            field_dict["railpack_version"] = railpack_version
        if base_digest is not UNSET:
            field_dict["base_digest"] = base_digest
        if source_url is not UNSET:
            field_dict["source_url"] = source_url
        if commit_sha is not UNSET:
            field_dict["commit_sha"] = commit_sha
        if runner_digest is not UNSET:
            field_dict["runner_digest"] = runner_digest
        if sbom_storage_key is not UNSET:
            field_dict["sbom_storage_key"] = sbom_storage_key
        if framework_version is not UNSET:
            field_dict["framework_version"] = framework_version

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        build_id = d.pop("build_id")

        source_sha256 = d.pop("source_sha256")

        plan = d.pop("plan")

        builder_node_id = d.pop("builder_node_id")

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        finished_at = datetime.datetime.fromisoformat(d.pop("finished_at"))

        buildkit_version = d.pop("buildkit_version", UNSET)

        railpack_version = d.pop("railpack_version", UNSET)

        base_digest = d.pop("base_digest", UNSET)

        source_url = d.pop("source_url", UNSET)

        commit_sha = d.pop("commit_sha", UNSET)

        runner_digest = d.pop("runner_digest", UNSET)

        def _parse_sbom_storage_key(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        sbom_storage_key = _parse_sbom_storage_key(d.pop("sbom_storage_key", UNSET))

        def _parse_framework_version(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        framework_version = _parse_framework_version(d.pop("framework_version", UNSET))

        build_provenance_response = cls(
            id=id,
            build_id=build_id,
            source_sha256=source_sha256,
            plan=plan,
            builder_node_id=builder_node_id,
            started_at=started_at,
            finished_at=finished_at,
            buildkit_version=buildkit_version,
            railpack_version=railpack_version,
            base_digest=base_digest,
            source_url=source_url,
            commit_sha=commit_sha,
            runner_digest=runner_digest,
            sbom_storage_key=sbom_storage_key,
            framework_version=framework_version,
        )

        build_provenance_response.additional_properties = d
        return build_provenance_response

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
