from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.project_environment_promotion_response import ProjectEnvironmentPromotionResponse
from ...models.promote_project_environment_request import PromoteProjectEnvironmentRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    *,
    body: PromoteProjectEnvironmentRequest,
    idempotency_key: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/projects/{slug}/environments/{environment}/promote".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProjectEnvironmentPromotionResponse | None:
    if response.status_code == 200:
        response_200 = ProjectEnvironmentPromotionResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ProjectEnvironmentPromotionResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: PromoteProjectEnvironmentRequest,
    idempotency_key: str,
) -> Response[Problem | ProjectEnvironmentPromotionResponse]:
    """Execute a guarded promotion between project environments.

     Revalidates the supplied promotion token against current live
    deployments and environment configuration before promoting immutable
    source artifacts. Protected targets also require an approval token
    issued for that exact promotion. After cutover, every target artifact
    is verified against its source release and target environment. A
    failed verification automatically rolls back the promotion. Target
    configuration and secrets are never copied from the source environment.

    Args:
        slug (str):
        environment (str):
        idempotency_key (str):
        body (PromoteProjectEnvironmentRequest): Request to execute one exact environment
            promotion.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentPromotionResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: PromoteProjectEnvironmentRequest,
    idempotency_key: str,
) -> Problem | ProjectEnvironmentPromotionResponse | None:
    """Execute a guarded promotion between project environments.

     Revalidates the supplied promotion token against current live
    deployments and environment configuration before promoting immutable
    source artifacts. Protected targets also require an approval token
    issued for that exact promotion. After cutover, every target artifact
    is verified against its source release and target environment. A
    failed verification automatically rolls back the promotion. Target
    configuration and secrets are never copied from the source environment.

    Args:
        slug (str):
        environment (str):
        idempotency_key (str):
        body (PromoteProjectEnvironmentRequest): Request to execute one exact environment
            promotion.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentPromotionResponse
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: PromoteProjectEnvironmentRequest,
    idempotency_key: str,
) -> Response[Problem | ProjectEnvironmentPromotionResponse]:
    """Execute a guarded promotion between project environments.

     Revalidates the supplied promotion token against current live
    deployments and environment configuration before promoting immutable
    source artifacts. Protected targets also require an approval token
    issued for that exact promotion. After cutover, every target artifact
    is verified against its source release and target environment. A
    failed verification automatically rolls back the promotion. Target
    configuration and secrets are never copied from the source environment.

    Args:
        slug (str):
        environment (str):
        idempotency_key (str):
        body (PromoteProjectEnvironmentRequest): Request to execute one exact environment
            promotion.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentPromotionResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: PromoteProjectEnvironmentRequest,
    idempotency_key: str,
) -> Problem | ProjectEnvironmentPromotionResponse | None:
    """Execute a guarded promotion between project environments.

     Revalidates the supplied promotion token against current live
    deployments and environment configuration before promoting immutable
    source artifacts. Protected targets also require an approval token
    issued for that exact promotion. After cutover, every target artifact
    is verified against its source release and target environment. A
    failed verification automatically rolls back the promotion. Target
    configuration and secrets are never copied from the source environment.

    Args:
        slug (str):
        environment (str):
        idempotency_key (str):
        body (PromoteProjectEnvironmentRequest): Request to execute one exact environment
            promotion.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentPromotionResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
