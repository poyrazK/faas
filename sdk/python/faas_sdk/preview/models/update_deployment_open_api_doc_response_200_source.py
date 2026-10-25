from typing import Literal

UpdateDeploymentOpenAPIDocResponse200Source = Literal["cold_boot", "manual_upload"]

UPDATE_DEPLOYMENT_OPEN_API_DOC_RESPONSE_200_SOURCE_VALUES: set[UpdateDeploymentOpenAPIDocResponse200Source] = {
    "cold_boot",
    "manual_upload",
}


def check_update_deployment_open_api_doc_response_200_source(value: str) -> UpdateDeploymentOpenAPIDocResponse200Source:
    if value in UPDATE_DEPLOYMENT_OPEN_API_DOC_RESPONSE_200_SOURCE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_DEPLOYMENT_OPEN_API_DOC_RESPONSE_200_SOURCE_VALUES!r}"
    )
