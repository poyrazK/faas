from typing import Literal

ListEnvValues = Literal["true"]

LIST_ENV_VALUES_VALUES: set[ListEnvValues] = {
    "true",
}


def check_list_env_values(value: str) -> ListEnvValues:
    if value in LIST_ENV_VALUES_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_ENV_VALUES_VALUES!r}")
