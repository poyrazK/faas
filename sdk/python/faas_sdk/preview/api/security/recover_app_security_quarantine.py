from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.security_quarantine_recovery_request import SecurityQuarantineRecoveryRequest
from ...models.security_quarantine_recovery_response import SecurityQuarantineRecoveryResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: SecurityQuarantineRecoveryRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/security/recover".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | SecurityQuarantineRecoveryResponse | None:
    if response.status_code == 200:
        response_200 = SecurityQuarantineRecoveryResponse.from_dict(response.json())

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

    if response.status_code == 500:
        response_500 = Problem.from_dict(response.json())

        return response_500

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | SecurityQuarantineRecoveryResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SecurityQuarantineRecoveryRequest,
) -> Response[Problem | SecurityQuarantineRecoveryResponse]:
    """Recover an app from image-scan quarantine.

     Restores an app to active only when the selected deployment is live,
    newer than the recorded security quarantine, uses a different image
    digest, and carries complete digest-matched scan evidence with zero
    HIGH, CRITICAL, or UNKNOWN findings. Every live canary deployment is
    checked before traffic is restored. The transition is idempotent and
    emits an audited security recovery event.
    Requires deploy-write scope and MFA.

    Args:
        slug (str):
        body (SecurityQuarantineRecoveryRequest): The clean live deployment to use when recovering
            an app from image-scan quarantine.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SecurityQuarantineRecoveryResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SecurityQuarantineRecoveryRequest,
) -> Problem | SecurityQuarantineRecoveryResponse | None:
    """Recover an app from image-scan quarantine.

     Restores an app to active only when the selected deployment is live,
    newer than the recorded security quarantine, uses a different image
    digest, and carries complete digest-matched scan evidence with zero
    HIGH, CRITICAL, or UNKNOWN findings. Every live canary deployment is
    checked before traffic is restored. The transition is idempotent and
    emits an audited security recovery event.
    Requires deploy-write scope and MFA.

    Args:
        slug (str):
        body (SecurityQuarantineRecoveryRequest): The clean live deployment to use when recovering
            an app from image-scan quarantine.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SecurityQuarantineRecoveryResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SecurityQuarantineRecoveryRequest,
) -> Response[Problem | SecurityQuarantineRecoveryResponse]:
    """Recover an app from image-scan quarantine.

     Restores an app to active only when the selected deployment is live,
    newer than the recorded security quarantine, uses a different image
    digest, and carries complete digest-matched scan evidence with zero
    HIGH, CRITICAL, or UNKNOWN findings. Every live canary deployment is
    checked before traffic is restored. The transition is idempotent and
    emits an audited security recovery event.
    Requires deploy-write scope and MFA.

    Args:
        slug (str):
        body (SecurityQuarantineRecoveryRequest): The clean live deployment to use when recovering
            an app from image-scan quarantine.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SecurityQuarantineRecoveryResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SecurityQuarantineRecoveryRequest,
) -> Problem | SecurityQuarantineRecoveryResponse | None:
    """Recover an app from image-scan quarantine.

     Restores an app to active only when the selected deployment is live,
    newer than the recorded security quarantine, uses a different image
    digest, and carries complete digest-matched scan evidence with zero
    HIGH, CRITICAL, or UNKNOWN findings. Every live canary deployment is
    checked before traffic is restored. The transition is idempotent and
    emits an audited security recovery event.
    Requires deploy-write scope and MFA.

    Args:
        slug (str):
        body (SecurityQuarantineRecoveryRequest): The clean live deployment to use when recovering
            an app from image-scan quarantine.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SecurityQuarantineRecoveryResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
