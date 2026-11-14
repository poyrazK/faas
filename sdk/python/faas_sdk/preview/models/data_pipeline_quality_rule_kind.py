from typing import Literal

DataPipelineQualityRuleKind = Literal["range", "reference", "required", "unique"]

DATA_PIPELINE_QUALITY_RULE_KIND_VALUES: set[DataPipelineQualityRuleKind] = {
    "range",
    "reference",
    "required",
    "unique",
}


def check_data_pipeline_quality_rule_kind(value: str) -> DataPipelineQualityRuleKind:
    if value in DATA_PIPELINE_QUALITY_RULE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_PIPELINE_QUALITY_RULE_KIND_VALUES!r}")
