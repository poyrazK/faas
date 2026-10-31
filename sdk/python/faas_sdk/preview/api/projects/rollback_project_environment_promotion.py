from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.project_environment_promotion_status_response import ProjectEnvironmentPromotionStatusResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    promotion: str,
    *,
    idempotency_key: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/projects/{slug}/environments/{environment}/promotions/{promotion}/rollback".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
            promotion=quote(str(promotion), safe=""),
        ),
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProjectEnvironmentPromotionStatusResponse | None:
    if response.status_code == 200:
        response_200 = ProjectEnvironmentPromotionStatusResponse.from_dict(response.json())

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
) -> Response[Problem | ProjectEnvironmentPromotionStatusResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    environment: str,
    promotion: str,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str,
) -> Response[Problem | ProjectEnvironmentPromotionStatusResponse]:
    """Roll back a completed or failed project environment promotion.

     Restores each workload to the target deployment captured before the
    promotion. Workloads created by the promotion are failed and removed
    from traffic when there was no prior target. The operation refuses to
    overwrite a target changed after the promotion and records durable
    per-workload rollback checkpoints.

    Args:
        slug (str):
        environment (str):
        promotion (str):
        idempotency_key (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentPromotionStatusResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        promotion=promotion,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    environment: str,
    promotion: str,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str,
) -> Problem | ProjectEnvironmentPromotionStatusResponse | None:
    """Roll back a completed or failed project environment promotion.

     Restores each workload to the target deployment captured before the
    promotion. Workloads created by the promotion are failed and removed
    from traffic when there was no prior target. The operation refuses to
    overwrite a target changed after the promotion and records durable
    per-workload rollback checkpoints.

    Args:
        slug (str):
        environment (str):
        promotion (str):
        idempotency_key (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentPromotionStatusResponse
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        promotion=promotion,
        client=client,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    promotion: str,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str,
) -> Response[Problem | ProjectEnvironmentPromotionStatusResponse]:
    """Roll back a completed or failed project environment promotion.

     Restores each workload to the target deployment captured before the
    promotion. Workloads created by the promotion are failed and removed
    from traffic when there was no prior target. The operation refuses to
    overwrite a target changed after the promotion and records durable
    per-workload rollback checkpoints.

    Args:
        slug (str):
        environment (str):
        promotion (str):
        idempotency_key (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentPromotionStatusResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        promotion=promotion,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    promotion: str,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str,
) -> Problem | ProjectEnvironmentPromotionStatusResponse | None:
    """Roll back a completed or failed project environment promotion.

     Restores each workload to the target deployment captured before the
    promotion. Workloads created by the promotion are failed and removed
    from traffic when there was no prior target. The operation refuses to
    overwrite a target changed after the promotion and records durable
    per-workload rollback checkpoints.

    Args:
        slug (str):
        environment (str):
        promotion (str):
        idempotency_key (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentPromotionStatusResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            promotion=promotion,
            client=client,
            idempotency_key=idempotency_key,
        )
    ).parsed
