from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="EnvironmentWorkloadActivationEvidence")


@_attrs_define
class EnvironmentWorkloadActivationEvidence:
    """Evidence summary for the exact current reviewed workload graph; retained workloads count only when their exact live
    deployment remains in the active release set. This evidence does not authorize candidate activation or serving.

    """

    graph_id: UUID
    source_id: UUID
    environment_id: UUID
    revision_id: UUID
    definition_digest: str
    generation: int
    intent_version: int
    plan_hash: str
    graph_phase: str
    artifacts_prepared: bool
    candidates: int
    retained_workloads_recorded: int
    """Unchanged graph members verified against their exact live active release-set deployments."""
    captures_recorded: int
    guest_config_acknowledgements_recorded: int
    restores_recorded: int
    smokes_recorded: int
    job_smokes_recorded: int
    framework_ready_acknowledgements_recorded: int
    qualified: bool
    activated: bool
    serving: bool
    blocking_reasons: list[str]
    graph_error_code: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        graph_id = str(self.graph_id)

        source_id = str(self.source_id)

        environment_id = str(self.environment_id)

        revision_id = str(self.revision_id)

        definition_digest = self.definition_digest

        generation = self.generation

        intent_version = self.intent_version

        plan_hash = self.plan_hash

        graph_phase = self.graph_phase

        artifacts_prepared = self.artifacts_prepared

        candidates = self.candidates

        retained_workloads_recorded = self.retained_workloads_recorded

        captures_recorded = self.captures_recorded

        guest_config_acknowledgements_recorded = self.guest_config_acknowledgements_recorded

        restores_recorded = self.restores_recorded

        smokes_recorded = self.smokes_recorded

        job_smokes_recorded = self.job_smokes_recorded

        framework_ready_acknowledgements_recorded = self.framework_ready_acknowledgements_recorded

        qualified = self.qualified

        activated = self.activated

        serving = self.serving

        blocking_reasons = self.blocking_reasons

        graph_error_code = self.graph_error_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "graph_id": graph_id,
                "source_id": source_id,
                "environment_id": environment_id,
                "revision_id": revision_id,
                "definition_digest": definition_digest,
                "generation": generation,
                "intent_version": intent_version,
                "plan_hash": plan_hash,
                "graph_phase": graph_phase,
                "artifacts_prepared": artifacts_prepared,
                "candidates": candidates,
                "retained_workloads_recorded": retained_workloads_recorded,
                "captures_recorded": captures_recorded,
                "guest_config_acknowledgements_recorded": guest_config_acknowledgements_recorded,
                "restores_recorded": restores_recorded,
                "smokes_recorded": smokes_recorded,
                "job_smokes_recorded": job_smokes_recorded,
                "framework_ready_acknowledgements_recorded": framework_ready_acknowledgements_recorded,
                "qualified": qualified,
                "activated": activated,
                "serving": serving,
                "blocking_reasons": blocking_reasons,
            }
        )
        if graph_error_code is not UNSET:
            field_dict["graph_error_code"] = graph_error_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        graph_id = UUID(d.pop("graph_id"))

        source_id = UUID(d.pop("source_id"))

        environment_id = UUID(d.pop("environment_id"))

        revision_id = UUID(d.pop("revision_id"))

        definition_digest = d.pop("definition_digest")

        generation = d.pop("generation")

        intent_version = d.pop("intent_version")

        plan_hash = d.pop("plan_hash")

        graph_phase = d.pop("graph_phase")

        artifacts_prepared = d.pop("artifacts_prepared")

        candidates = d.pop("candidates")

        retained_workloads_recorded = d.pop("retained_workloads_recorded")

        captures_recorded = d.pop("captures_recorded")

        guest_config_acknowledgements_recorded = d.pop("guest_config_acknowledgements_recorded")

        restores_recorded = d.pop("restores_recorded")

        smokes_recorded = d.pop("smokes_recorded")

        job_smokes_recorded = d.pop("job_smokes_recorded")

        framework_ready_acknowledgements_recorded = d.pop("framework_ready_acknowledgements_recorded")

        qualified = d.pop("qualified")

        activated = d.pop("activated")

        serving = d.pop("serving")

        blocking_reasons = cast(list[str], d.pop("blocking_reasons"))

        graph_error_code = d.pop("graph_error_code", UNSET)

        environment_workload_activation_evidence = cls(
            graph_id=graph_id,
            source_id=source_id,
            environment_id=environment_id,
            revision_id=revision_id,
            definition_digest=definition_digest,
            generation=generation,
            intent_version=intent_version,
            plan_hash=plan_hash,
            graph_phase=graph_phase,
            artifacts_prepared=artifacts_prepared,
            candidates=candidates,
            retained_workloads_recorded=retained_workloads_recorded,
            captures_recorded=captures_recorded,
            guest_config_acknowledgements_recorded=guest_config_acknowledgements_recorded,
            restores_recorded=restores_recorded,
            smokes_recorded=smokes_recorded,
            job_smokes_recorded=job_smokes_recorded,
            framework_ready_acknowledgements_recorded=framework_ready_acknowledgements_recorded,
            qualified=qualified,
            activated=activated,
            serving=serving,
            blocking_reasons=blocking_reasons,
            graph_error_code=graph_error_code,
        )

        environment_workload_activation_evidence.additional_properties = d
        return environment_workload_activation_evidence

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
