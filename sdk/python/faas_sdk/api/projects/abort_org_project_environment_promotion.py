from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.project_promotion_control import ProjectPromotionControl
from ...models.project_promotion_control_request import ProjectPromotionControlRequest
from ...types import Response


def _get_kwargs(
    organization: str,
    slug: str,
    environment: str,
    promotion: str,
    *,
    body: ProjectPromotionControlRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/orgs/{organization}/projects/{slug}/environments/{environment}/promotions/{promotion}/abort".format(
            organization=quote(str(organization), safe=""),
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
            promotion=quote(str(promotion), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | Problem | ProjectPromotionControl | None:
    if response.status_code == 200:
        response_200 = ProjectPromotionControl.from_dict(response.json())

        return response_200

    if response.status_code == 202:
        response_202 = ProjectPromotionControl.from_dict(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
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
) -> Response[Any | Problem | ProjectPromotionControl]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    organization: str,
    slug: str,
    environment: str,
    promotion: str,
    *,
    client: AuthenticatedClient | Client,
    body: ProjectPromotionControlRequest,
) -> Response[Any | Problem | ProjectPromotionControl]:
    """Abort an in-progress company environment promotion.

     Company form of the generation-fenced promotion abort. Requires developer, admin or owner
    membership.

    Args:
        organization (str):
        slug (str):
        environment (str):
        promotion (str):
        body (ProjectPromotionControlRequest): Stable operator request identity and expected
            promotion checkpoint generation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem | ProjectPromotionControl]
    """

    kwargs = _get_kwargs(
        organization=organization,
        slug=slug,
        environment=environment,
        promotion=promotion,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    organization: str,
    slug: str,
    environment: str,
    promotion: str,
    *,
    client: AuthenticatedClient | Client,
    body: ProjectPromotionControlRequest,
) -> Any | Problem | ProjectPromotionControl | None:
    """Abort an in-progress company environment promotion.

     Company form of the generation-fenced promotion abort. Requires developer, admin or owner
    membership.

    Args:
        organization (str):
        slug (str):
        environment (str):
        promotion (str):
        body (ProjectPromotionControlRequest): Stable operator request identity and expected
            promotion checkpoint generation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem | ProjectPromotionControl
    """

    return sync_detailed(
        organization=organization,
        slug=slug,
        environment=environment,
        promotion=promotion,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    organization: str,
    slug: str,
    environment: str,
    promotion: str,
    *,
    client: AuthenticatedClient | Client,
    body: ProjectPromotionControlRequest,
) -> Response[Any | Problem | ProjectPromotionControl]:
    """Abort an in-progress company environment promotion.

     Company form of the generation-fenced promotion abort. Requires developer, admin or owner
    membership.

    Args:
        organization (str):
        slug (str):
        environment (str):
        promotion (str):
        body (ProjectPromotionControlRequest): Stable operator request identity and expected
            promotion checkpoint generation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem | ProjectPromotionControl]
    """

    kwargs = _get_kwargs(
        organization=organization,
        slug=slug,
        environment=environment,
        promotion=promotion,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    slug: str,
    environment: str,
    promotion: str,
    *,
    client: AuthenticatedClient | Client,
    body: ProjectPromotionControlRequest,
) -> Any | Problem | ProjectPromotionControl | None:
    """Abort an in-progress company environment promotion.

     Company form of the generation-fenced promotion abort. Requires developer, admin or owner
    membership.

    Args:
        organization (str):
        slug (str):
        environment (str):
        promotion (str):
        body (ProjectPromotionControlRequest): Stable operator request identity and expected
            promotion checkpoint generation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem | ProjectPromotionControl
    """

    return (
        await asyncio_detailed(
            organization=organization,
            slug=slug,
            environment=environment,
            promotion=promotion,
            client=client,
            body=body,
        )
    ).parsed
