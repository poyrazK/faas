from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.flag_experiment import FlagExperiment
    from ..models.flag_experiment_arm import FlagExperimentArm
    from ..models.flag_experiment_bandit import FlagExperimentBandit
    from ..models.flag_experiment_cuped import FlagExperimentCuped
    from ..models.flag_experiment_guardrail import FlagExperimentGuardrail
    from ..models.flag_experiment_latency import FlagExperimentLatency
    from ..models.flag_experiment_recommendation import FlagExperimentRecommendation
    from ..models.flag_experiment_segment import FlagExperimentSegment
    from ..models.flag_experiment_variant_result import FlagExperimentVariantResult


T = TypeVar("T", bound="FlagExperimentResults")


@_attrs_define
class FlagExperimentResults:
    """A flag experiment's sequential results."""

    experiment: FlagExperiment
    """A flag experiment."""
    control: FlagExperimentArm
    """One variant's exposed customers; rate is the conversion rate, or the mean value per customer for mean
    metrics."""
    variants: list[FlagExperimentVariantResult]
    sample_ratio_p_value: float
    sample_ratio_mismatch: bool
    crossed_units: int
    """Customers that saw more than one variant and are left out."""
    cuped: FlagExperimentCuped
    """CUPED variance reduction on the results; reason says why it was not applied."""
    guardrails: list[FlagExperimentGuardrail] | Unset = UNSET
    latency: list[FlagExperimentLatency] | Unset = UNSET
    recommendation: FlagExperimentRecommendation | Unset = UNSET
    """What to do with the results; commands carry it out. Gregale never ships a variant on its own."""
    fixed_horizon: bool | Unset = UNSET
    """Percentile results: bootstrap p-values without a sequential guarantee; read them once, at the planned end."""
    bandit: FlagExperimentBandit | Unset = UNSET
    """A bandit's posterior per arm and when it last re-weighted the rule."""
    segments: list[FlagExperimentSegment] | Unset = UNSET
    """Results split by ?segment=COHORT: its members, then everyone else."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        experiment = self.experiment.to_dict()

        control = self.control.to_dict()

        variants = []
        for variants_item_data in self.variants:
            variants_item = variants_item_data.to_dict()
            variants.append(variants_item)

        sample_ratio_p_value = self.sample_ratio_p_value

        sample_ratio_mismatch = self.sample_ratio_mismatch

        crossed_units = self.crossed_units

        cuped = self.cuped.to_dict()

        guardrails: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.guardrails, Unset):
            guardrails = []
            for guardrails_item_data in self.guardrails:
                guardrails_item = guardrails_item_data.to_dict()
                guardrails.append(guardrails_item)

        latency: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.latency, Unset):
            latency = []
            for latency_item_data in self.latency:
                latency_item = latency_item_data.to_dict()
                latency.append(latency_item)

        recommendation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.recommendation, Unset):
            recommendation = self.recommendation.to_dict()

        fixed_horizon = self.fixed_horizon

        bandit: dict[str, Any] | Unset = UNSET
        if not isinstance(self.bandit, Unset):
            bandit = self.bandit.to_dict()

        segments: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.segments, Unset):
            segments = []
            for segments_item_data in self.segments:
                segments_item = segments_item_data.to_dict()
                segments.append(segments_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "experiment": experiment,
                "control": control,
                "variants": variants,
                "sample_ratio_p_value": sample_ratio_p_value,
                "sample_ratio_mismatch": sample_ratio_mismatch,
                "crossed_units": crossed_units,
                "cuped": cuped,
            }
        )
        if guardrails is not UNSET:
            field_dict["guardrails"] = guardrails
        if latency is not UNSET:
            field_dict["latency"] = latency
        if recommendation is not UNSET:
            field_dict["recommendation"] = recommendation
        if fixed_horizon is not UNSET:
            field_dict["fixed_horizon"] = fixed_horizon
        if bandit is not UNSET:
            field_dict["bandit"] = bandit
        if segments is not UNSET:
            field_dict["segments"] = segments

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_experiment import FlagExperiment
        from ..models.flag_experiment_arm import FlagExperimentArm
        from ..models.flag_experiment_bandit import FlagExperimentBandit
        from ..models.flag_experiment_cuped import FlagExperimentCuped
        from ..models.flag_experiment_guardrail import FlagExperimentGuardrail
        from ..models.flag_experiment_latency import FlagExperimentLatency
        from ..models.flag_experiment_recommendation import FlagExperimentRecommendation
        from ..models.flag_experiment_segment import FlagExperimentSegment
        from ..models.flag_experiment_variant_result import FlagExperimentVariantResult

        d = dict(src_dict)
        experiment = FlagExperiment.from_dict(d.pop("experiment"))

        control = FlagExperimentArm.from_dict(d.pop("control"))

        variants = []
        _variants = d.pop("variants")
        for variants_item_data in _variants:
            variants_item = FlagExperimentVariantResult.from_dict(variants_item_data)

            variants.append(variants_item)

        sample_ratio_p_value = d.pop("sample_ratio_p_value")

        sample_ratio_mismatch = d.pop("sample_ratio_mismatch")

        crossed_units = d.pop("crossed_units")

        cuped = FlagExperimentCuped.from_dict(d.pop("cuped"))

        _guardrails = d.pop("guardrails", UNSET)
        guardrails: list[FlagExperimentGuardrail] | Unset = UNSET
        if _guardrails is not UNSET:
            guardrails = []
            for guardrails_item_data in _guardrails:
                guardrails_item = FlagExperimentGuardrail.from_dict(guardrails_item_data)

                guardrails.append(guardrails_item)

        _latency = d.pop("latency", UNSET)
        latency: list[FlagExperimentLatency] | Unset = UNSET
        if _latency is not UNSET:
            latency = []
            for latency_item_data in _latency:
                latency_item = FlagExperimentLatency.from_dict(latency_item_data)

                latency.append(latency_item)

        _recommendation = d.pop("recommendation", UNSET)
        recommendation: FlagExperimentRecommendation | Unset
        if isinstance(_recommendation, Unset):
            recommendation = UNSET
        else:
            recommendation = FlagExperimentRecommendation.from_dict(_recommendation)

        fixed_horizon = d.pop("fixed_horizon", UNSET)

        _bandit = d.pop("bandit", UNSET)
        bandit: FlagExperimentBandit | Unset
        if isinstance(_bandit, Unset):
            bandit = UNSET
        else:
            bandit = FlagExperimentBandit.from_dict(_bandit)

        _segments = d.pop("segments", UNSET)
        segments: list[FlagExperimentSegment] | Unset = UNSET
        if _segments is not UNSET:
            segments = []
            for segments_item_data in _segments:
                segments_item = FlagExperimentSegment.from_dict(segments_item_data)

                segments.append(segments_item)

        flag_experiment_results = cls(
            experiment=experiment,
            control=control,
            variants=variants,
            sample_ratio_p_value=sample_ratio_p_value,
            sample_ratio_mismatch=sample_ratio_mismatch,
            crossed_units=crossed_units,
            cuped=cuped,
            guardrails=guardrails,
            latency=latency,
            recommendation=recommendation,
            fixed_horizon=fixed_horizon,
            bandit=bandit,
            segments=segments,
        )

        flag_experiment_results.additional_properties = d
        return flag_experiment_results

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
