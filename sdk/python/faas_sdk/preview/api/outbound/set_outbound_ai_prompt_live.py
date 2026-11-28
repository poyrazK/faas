from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.outbound_ai_prompt import OutboundAIPrompt
from ...models.problem import Problem
from ...models.put_outbound_ai_prompt_live_request import PutOutboundAIPromptLiveRequest
from ...types import Response


def _get_kwargs(
    prompt: str,
    *,
    body: PutOutboundAIPromptLiveRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/outbound/ai-prompts/{prompt}/live".format(
            prompt=quote(str(prompt), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OutboundAIPrompt | Problem | None:
    if response.status_code == 200:
        response_200 = OutboundAIPrompt.from_dict(response.json())

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
) -> Response[OutboundAIPrompt | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    prompt: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutOutboundAIPromptLiveRequest,
) -> Response[OutboundAIPrompt | Problem]:
    """Make a managed AI prompt version live.

     Requires MFA and deploy-write scope. The old live version becomes the previous one and any canary
    ends.

    Args:
        prompt (str):
        body (PutOutboundAIPromptLiveRequest): The version to make live.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundAIPrompt | Problem]
    """

    kwargs = _get_kwargs(
        prompt=prompt,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    prompt: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutOutboundAIPromptLiveRequest,
) -> OutboundAIPrompt | Problem | None:
    """Make a managed AI prompt version live.

     Requires MFA and deploy-write scope. The old live version becomes the previous one and any canary
    ends.

    Args:
        prompt (str):
        body (PutOutboundAIPromptLiveRequest): The version to make live.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundAIPrompt | Problem
    """

    return sync_detailed(
        prompt=prompt,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    prompt: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutOutboundAIPromptLiveRequest,
) -> Response[OutboundAIPrompt | Problem]:
    """Make a managed AI prompt version live.

     Requires MFA and deploy-write scope. The old live version becomes the previous one and any canary
    ends.

    Args:
        prompt (str):
        body (PutOutboundAIPromptLiveRequest): The version to make live.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundAIPrompt | Problem]
    """

    kwargs = _get_kwargs(
        prompt=prompt,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    prompt: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutOutboundAIPromptLiveRequest,
) -> OutboundAIPrompt | Problem | None:
    """Make a managed AI prompt version live.

     Requires MFA and deploy-write scope. The old live version becomes the previous one and any canary
    ends.

    Args:
        prompt (str):
        body (PutOutboundAIPromptLiveRequest): The version to make live.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundAIPrompt | Problem
    """

    return (
        await asyncio_detailed(
            prompt=prompt,
            client=client,
            body=body,
        )
    ).parsed
