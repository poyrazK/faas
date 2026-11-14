from typing import Literal

DataPipelineEvolutionRequestKind = Literal["auth_organizations", "projection", "relationships"]

DATA_PIPELINE_EVOLUTION_REQUEST_KIND_VALUES: set[DataPipelineEvolutionRequestKind] = {
    "auth_organizations",
    "projection",
    "relationships",
}


def check_data_pipeline_evolution_request_kind(value: str) -> DataPipelineEvolutionRequestKind:
    if value in DATA_PIPELINE_EVOLUTION_REQUEST_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_PIPELINE_EVOLUTION_REQUEST_KIND_VALUES!r}")
