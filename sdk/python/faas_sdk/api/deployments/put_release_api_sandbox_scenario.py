from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_sandbox_scenario_response import APISandboxScenarioResponse
from ...models.api_sandbox_scenario_save_request import APISandboxScenarioSaveRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    deployment: UUID,
    *,
    body: APISandboxScenarioSaveRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/deployments/{deployment}/api-sandbox-scenario".format(
            slug=quote(str(slug), safe=""),
            deployment=quote(str(deployment), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APISandboxScenarioResponse | Problem | None:
    if response.status_code == 200:
        response_200 = APISandboxScenarioResponse.from_dict(response.json())

        return response_200

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
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APISandboxScenarioResponse | Problem]:
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
    body: APISandboxScenarioSaveRequest,
) -> Response[APISandboxScenarioResponse | Problem]:
    """Save the API sandbox scenario for a release.

     Changes the scenario used for future copies. Existing sandboxes keep their original scenario.

    Args:
        slug (str):
        deployment (UUID):
        body (APISandboxScenarioSaveRequest): Definition and optional revision precondition for
            saving a release scenario.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxScenarioResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        body=body,
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
    body: APISandboxScenarioSaveRequest,
) -> APISandboxScenarioResponse | Problem | None:
    """Save the API sandbox scenario for a release.

     Changes the scenario used for future copies. Existing sandboxes keep their original scenario.

    Args:
        slug (str):
        deployment (UUID):
        body (APISandboxScenarioSaveRequest): Definition and optional revision precondition for
            saving a release scenario.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxScenarioResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        deployment=deployment,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxScenarioSaveRequest,
) -> Response[APISandboxScenarioResponse | Problem]:
    """Save the API sandbox scenario for a release.

     Changes the scenario used for future copies. Existing sandboxes keep their original scenario.

    Args:
        slug (str):
        deployment (UUID):
        body (APISandboxScenarioSaveRequest): Definition and optional revision precondition for
            saving a release scenario.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxScenarioResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxScenarioSaveRequest,
) -> APISandboxScenarioResponse | Problem | None:
    """Save the API sandbox scenario for a release.

     Changes the scenario used for future copies. Existing sandboxes keep their original scenario.

    Args:
        slug (str):
        deployment (UUID):
        body (APISandboxScenarioSaveRequest): Definition and optional revision precondition for
            saving a release scenario.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxScenarioResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment=deployment,
            client=client,
            body=body,
        )
    ).parsed
