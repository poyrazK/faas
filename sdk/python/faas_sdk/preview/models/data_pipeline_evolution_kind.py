from typing import Literal

DataPipelineEvolutionKind = Literal["auth_organizations", "projection", "relationships"]

DATA_PIPELINE_EVOLUTION_KIND_VALUES: set[DataPipelineEvolutionKind] = {
    "auth_organizations",
    "projection",
    "relationships",
}


def check_data_pipeline_evolution_kind(value: str) -> DataPipelineEvolutionKind:
    if value in DATA_PIPELINE_EVOLUTION_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_PIPELINE_EVOLUTION_KIND_VALUES!r}")
