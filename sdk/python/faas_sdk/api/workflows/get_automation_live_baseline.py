from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.automation_live_baseline import AutomationLiveBaseline
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    name: str,
    baseline: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/automations/{name}/live-baselines/{baseline}".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
            baseline=quote(str(baseline), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AutomationLiveBaseline | Problem | None:
    if response.status_code == 200:
        response_200 = AutomationLiveBaseline.from_dict(response.json())

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

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AutomationLiveBaseline | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    name: str,
    baseline: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AutomationLiveBaseline | Problem]:
    """Read the current named automation baseline promotion receipt.

     Read-only; requires app read scope. Records the exact suite, revision, actor, promotion time and
    reviewed coverage/age policy.

    Args:
        slug (str):
        name (str):
        baseline (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AutomationLiveBaseline | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        baseline=baseline,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    name: str,
    baseline: str,
    *,
    client: AuthenticatedClient | Client,
) -> AutomationLiveBaseline | Problem | None:
    """Read the current named automation baseline promotion receipt.

     Read-only; requires app read scope. Records the exact suite, revision, actor, promotion time and
    reviewed coverage/age policy.

    Args:
        slug (str):
        name (str):
        baseline (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AutomationLiveBaseline | Problem
    """

    return sync_detailed(
        slug=slug,
        name=name,
        baseline=baseline,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    baseline: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AutomationLiveBaseline | Problem]:
    """Read the current named automation baseline promotion receipt.

     Read-only; requires app read scope. Records the exact suite, revision, actor, promotion time and
    reviewed coverage/age policy.

    Args:
        slug (str):
        name (str):
        baseline (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AutomationLiveBaseline | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        baseline=baseline,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    baseline: str,
    *,
    client: AuthenticatedClient | Client,
) -> AutomationLiveBaseline | Problem | None:
    """Read the current named automation baseline promotion receipt.

     Read-only; requires app read scope. Records the exact suite, revision, actor, promotion time and
    reviewed coverage/age policy.

    Args:
        slug (str):
        name (str):
        baseline (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AutomationLiveBaseline | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            baseline=baseline,
            client=client,
        )
    ).parsed
