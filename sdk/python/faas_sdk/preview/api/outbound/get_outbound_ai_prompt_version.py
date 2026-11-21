from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.outbound_ai_prompt_version import OutboundAIPromptVersion
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    prompt: str,
    version: int,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/outbound/ai-prompts/{prompt}/versions/{version}".format(
            prompt=quote(str(prompt), safe=""),
            version=quote(str(version), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OutboundAIPromptVersion | Problem | None:
    if response.status_code == 200:
        response_200 = OutboundAIPromptVersion.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OutboundAIPromptVersion | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    prompt: str,
    version: int,
    *,
    client: AuthenticatedClient | Client,
) -> Response[OutboundAIPromptVersion | Problem]:
    """Read a managed AI prompt version.

     Requires MFA and read scope. Returns its messages, variables, defaults, target and params.

    Args:
        prompt (str):
        version (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundAIPromptVersion | Problem]
    """

    kwargs = _get_kwargs(
        prompt=prompt,
        version=version,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    prompt: str,
    version: int,
    *,
    client: AuthenticatedClient | Client,
) -> OutboundAIPromptVersion | Problem | None:
    """Read a managed AI prompt version.

     Requires MFA and read scope. Returns its messages, variables, defaults, target and params.

    Args:
        prompt (str):
        version (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundAIPromptVersion | Problem
    """

    return sync_detailed(
        prompt=prompt,
        version=version,
        client=client,
    ).parsed


async def asyncio_detailed(
    prompt: str,
    version: int,
    *,
    client: AuthenticatedClient | Client,
) -> Response[OutboundAIPromptVersion | Problem]:
    """Read a managed AI prompt version.

     Requires MFA and read scope. Returns its messages, variables, defaults, target and params.

    Args:
        prompt (str):
        version (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundAIPromptVersion | Problem]
    """

    kwargs = _get_kwargs(
        prompt=prompt,
        version=version,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    prompt: str,
    version: int,
    *,
    client: AuthenticatedClient | Client,
) -> OutboundAIPromptVersion | Problem | None:
    """Read a managed AI prompt version.

     Requires MFA and read scope. Returns its messages, variables, defaults, target and params.

    Args:
        prompt (str):
        version (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundAIPromptVersion | Problem
    """

    return (
        await asyncio_detailed(
            prompt=prompt,
            version=version,
            client=client,
        )
    ).parsed
