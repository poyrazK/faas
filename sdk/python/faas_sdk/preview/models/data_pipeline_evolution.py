from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.data_pipeline_evolution_kind import DataPipelineEvolutionKind, check_data_pipeline_evolution_kind
from ..models.data_pipeline_evolution_state import DataPipelineEvolutionState, check_data_pipeline_evolution_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_pipeline_serving import DataPipelineServing


T = TypeVar("T", bound="DataPipelineEvolution")


@_attrs_define
class DataPipelineEvolution:
    """Frozen account and generation-bound serving preview. Approved indicates owner intent; applied requires worker
    verification and any required API cache refresh.

    """

    id: str
    state: DataPipelineEvolutionState
    kind: DataPipelineEvolutionKind
    generation: int
    preview_revision: int
    after: DataPipelineServing
    """Versioned serving contract affecting publication, mirror schema and recovery. Creation accepts version 1;
    approved evolution installs version 2. Omit columns for all-column replication. Tenant claims grant no
    memberships. Column expansion requires source-definition approval, backfill, verification and API refresh."""
    retry: int | Unset = UNSET
    before: DataPipelineServing | Unset = UNSET
    """Versioned serving contract affecting publication, mirror schema and recovery. Creation accepts version 1;
    approved evolution installs version 2. Omit columns for all-column replication. Tenant claims grant no
    memberships. Column expansion requires source-definition approval, backfill, verification and API refresh."""
    added_tables: list[str] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        state: str = self.state

        kind: str = self.kind

        generation = self.generation

        preview_revision = self.preview_revision

        after = self.after.to_dict()

        retry = self.retry

        before: dict[str, Any] | Unset = UNSET
        if not isinstance(self.before, Unset):
            before = self.before.to_dict()

        added_tables: list[str] | Unset = UNSET
        if not isinstance(self.added_tables, Unset):
            added_tables = self.added_tables

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "state": state,
                "kind": kind,
                "generation": generation,
                "preview_revision": preview_revision,
                "after": after,
            }
        )
        if retry is not UNSET:
            field_dict["retry"] = retry
        if before is not UNSET:
            field_dict["before"] = before
        if added_tables is not UNSET:
            field_dict["added_tables"] = added_tables

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_pipeline_serving import DataPipelineServing

        d = dict(src_dict)
        id = d.pop("id")

        state = check_data_pipeline_evolution_state(d.pop("state"))

        kind = check_data_pipeline_evolution_kind(d.pop("kind"))

        generation = d.pop("generation")

        preview_revision = d.pop("preview_revision")

        after = DataPipelineServing.from_dict(d.pop("after"))

        retry = d.pop("retry", UNSET)

        _before = d.pop("before", UNSET)
        before: DataPipelineServing | Unset
        if isinstance(_before, Unset):
            before = UNSET
        else:
            before = DataPipelineServing.from_dict(_before)

        added_tables = cast(list[str], d.pop("added_tables", UNSET))

        data_pipeline_evolution = cls(
            id=id,
            state=state,
            kind=kind,
            generation=generation,
            preview_revision=preview_revision,
            after=after,
            retry=retry,
            before=before,
            added_tables=added_tables,
        )

        return data_pipeline_evolution
