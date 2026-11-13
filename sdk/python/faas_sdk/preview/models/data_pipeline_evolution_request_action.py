from typing import Literal

DataPipelineEvolutionRequestAction = Literal["approve", "discard", "preview", "retry"]

DATA_PIPELINE_EVOLUTION_REQUEST_ACTION_VALUES: set[DataPipelineEvolutionRequestAction] = {
    "approve",
    "discard",
    "preview",
    "retry",
}


def check_data_pipeline_evolution_request_action(value: str) -> DataPipelineEvolutionRequestAction:
    if value in DATA_PIPELINE_EVOLUTION_REQUEST_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_PIPELINE_EVOLUTION_REQUEST_ACTION_VALUES!r}")
