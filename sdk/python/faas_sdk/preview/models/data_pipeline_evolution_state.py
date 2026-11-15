from typing import Literal

DataPipelineEvolutionState = Literal["applied", "approved", "preview", "superseded"]

DATA_PIPELINE_EVOLUTION_STATE_VALUES: set[DataPipelineEvolutionState] = {
    "applied",
    "approved",
    "preview",
    "superseded",
}


def check_data_pipeline_evolution_state(value: str) -> DataPipelineEvolutionState:
    if value in DATA_PIPELINE_EVOLUTION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_PIPELINE_EVOLUTION_STATE_VALUES!r}")
