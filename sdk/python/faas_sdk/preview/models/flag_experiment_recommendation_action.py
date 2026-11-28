from typing import Literal

FlagExperimentRecommendationAction = Literal[
    "decide", "fix_allocation", "keep_control", "keep_running", "rolled_back", "ship_variant"
]

FLAG_EXPERIMENT_RECOMMENDATION_ACTION_VALUES: set[FlagExperimentRecommendationAction] = {
    "decide",
    "fix_allocation",
    "keep_control",
    "keep_running",
    "rolled_back",
    "ship_variant",
}


def check_flag_experiment_recommendation_action(value: str) -> FlagExperimentRecommendationAction:
    if value in FLAG_EXPERIMENT_RECOMMENDATION_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_EXPERIMENT_RECOMMENDATION_ACTION_VALUES!r}")
