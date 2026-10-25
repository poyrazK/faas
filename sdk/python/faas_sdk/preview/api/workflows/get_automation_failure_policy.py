from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.automation_failure_policy_response import AutomationFailurePolicyResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    name: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/automations/{name}/failure-policy".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AutomationFailurePolicyResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AutomationFailurePolicyResponse.from_dict(response.json())

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

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AutomationFailurePolicyResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AutomationFailurePolicyResponse | Problem]:
    """Inspect failure pause policy and resume preview.

     Returns aggregate terminal outcomes since monitoring began or resumed, current pause generation,
    active run counts, retained event count and the latest 100 pause/resume transitions. Counts are
    advisory; no run payloads or error messages are included.

    Args:
        slug (str):
        name (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AutomationFailurePolicyResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
) -> AutomationFailurePolicyResponse | Problem | None:
    """Inspect failure pause policy and resume preview.

     Returns aggregate terminal outcomes since monitoring began or resumed, current pause generation,
    active run counts, retained event count and the latest 100 pause/resume transitions. Counts are
    advisory; no run payloads or error messages are included.

    Args:
        slug (str):
        name (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AutomationFailurePolicyResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        name=name,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AutomationFailurePolicyResponse | Problem]:
    """Inspect failure pause policy and resume preview.

     Returns aggregate terminal outcomes since monitoring began or resumed, current pause generation,
    active run counts, retained event count and the latest 100 pause/resume transitions. Counts are
    advisory; no run payloads or error messages are included.

    Args:
        slug (str):
        name (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AutomationFailurePolicyResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
) -> AutomationFailurePolicyResponse | Problem | None:
    """Inspect failure pause policy and resume preview.

     Returns aggregate terminal outcomes since monitoring began or resumed, current pause generation,
    active run counts, retained event count and the latest 100 pause/resume transitions. Counts are
    advisory; no run payloads or error messages are included.

    Args:
        slug (str):
        name (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AutomationFailurePolicyResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            client=client,
        )
    ).parsed
