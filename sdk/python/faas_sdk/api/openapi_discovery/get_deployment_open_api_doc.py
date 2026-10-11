from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.get_deployment_open_api_doc_response_200 import GetDeploymentOpenAPIDocResponse200
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    deployment: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/deployments/{deployment}/openapi".format(
            slug=quote(str(slug), safe=""),
            deployment=quote(str(deployment), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> GetDeploymentOpenAPIDocResponse200 | Problem | None:
    if response.status_code == 200:
        response_200 = GetDeploymentOpenAPIDocResponse200.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[GetDeploymentOpenAPIDocResponse200 | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[GetDeploymentOpenAPIDocResponse200 | Problem]:
    """Read the captured OpenAPI document for a deployment.

     Returns the OpenAPI document the cold-boot probe captured from the customer's app (issue #975 item
    #1, ADR-122). The probe runs unconditionally during cold boot; the apid surfaces the doc only on
    paid plans (Hobby/Pro/Scale). Free customers receive 402 + openapi_docs_not_allowed. Cache-Control:
    5 min. Response headers: X-OpenAPI-Doc-Source (cold_boot or manual_upload), X-OpenAPI-Doc-Truncated
    (1 if clipped at 128 KiB), X-OpenAPI-Doc-Byte-Size, X-OpenAPI-Doc-Deployment-ID, X-OpenAPI-Doc-App-
    ID, X-OpenAPI-Doc-SHA256, X-OpenAPI-Doc-Captured-At and X-OpenAPI-Doc-Updated-At. These
    authenticated metadata headers bind the unchanged body to its capture.

    Args:
        slug (str):
        deployment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GetDeploymentOpenAPIDocResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> GetDeploymentOpenAPIDocResponse200 | Problem | None:
    """Read the captured OpenAPI document for a deployment.

     Returns the OpenAPI document the cold-boot probe captured from the customer's app (issue #975 item
    #1, ADR-122). The probe runs unconditionally during cold boot; the apid surfaces the doc only on
    paid plans (Hobby/Pro/Scale). Free customers receive 402 + openapi_docs_not_allowed. Cache-Control:
    5 min. Response headers: X-OpenAPI-Doc-Source (cold_boot or manual_upload), X-OpenAPI-Doc-Truncated
    (1 if clipped at 128 KiB), X-OpenAPI-Doc-Byte-Size, X-OpenAPI-Doc-Deployment-ID, X-OpenAPI-Doc-App-
    ID, X-OpenAPI-Doc-SHA256, X-OpenAPI-Doc-Captured-At and X-OpenAPI-Doc-Updated-At. These
    authenticated metadata headers bind the unchanged body to its capture.

    Args:
        slug (str):
        deployment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GetDeploymentOpenAPIDocResponse200 | Problem
    """

    return sync_detailed(
        slug=slug,
        deployment=deployment,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[GetDeploymentOpenAPIDocResponse200 | Problem]:
    """Read the captured OpenAPI document for a deployment.

     Returns the OpenAPI document the cold-boot probe captured from the customer's app (issue #975 item
    #1, ADR-122). The probe runs unconditionally during cold boot; the apid surfaces the doc only on
    paid plans (Hobby/Pro/Scale). Free customers receive 402 + openapi_docs_not_allowed. Cache-Control:
    5 min. Response headers: X-OpenAPI-Doc-Source (cold_boot or manual_upload), X-OpenAPI-Doc-Truncated
    (1 if clipped at 128 KiB), X-OpenAPI-Doc-Byte-Size, X-OpenAPI-Doc-Deployment-ID, X-OpenAPI-Doc-App-
    ID, X-OpenAPI-Doc-SHA256, X-OpenAPI-Doc-Captured-At and X-OpenAPI-Doc-Updated-At. These
    authenticated metadata headers bind the unchanged body to its capture.

    Args:
        slug (str):
        deployment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GetDeploymentOpenAPIDocResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> GetDeploymentOpenAPIDocResponse200 | Problem | None:
    """Read the captured OpenAPI document for a deployment.

     Returns the OpenAPI document the cold-boot probe captured from the customer's app (issue #975 item
    #1, ADR-122). The probe runs unconditionally during cold boot; the apid surfaces the doc only on
    paid plans (Hobby/Pro/Scale). Free customers receive 402 + openapi_docs_not_allowed. Cache-Control:
    5 min. Response headers: X-OpenAPI-Doc-Source (cold_boot or manual_upload), X-OpenAPI-Doc-Truncated
    (1 if clipped at 128 KiB), X-OpenAPI-Doc-Byte-Size, X-OpenAPI-Doc-Deployment-ID, X-OpenAPI-Doc-App-
    ID, X-OpenAPI-Doc-SHA256, X-OpenAPI-Doc-Captured-At and X-OpenAPI-Doc-Updated-At. These
    authenticated metadata headers bind the unchanged body to its capture.

    Args:
        slug (str):
        deployment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GetDeploymentOpenAPIDocResponse200 | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment=deployment,
            client=client,
        )
    ).parsed
