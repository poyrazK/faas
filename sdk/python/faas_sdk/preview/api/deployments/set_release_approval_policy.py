from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.release_approval_policy import ReleaseApprovalPolicy
from ...models.set_release_approval_policy_request import SetReleaseApprovalPolicyRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: SetReleaseApprovalPolicyRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/release-approval-policy".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ReleaseApprovalPolicy | None:
    if response.status_code == 200:
        response_200 = ReleaseApprovalPolicy.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ReleaseApprovalPolicy]:
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
    body: SetReleaseApprovalPolicyRequest,
) -> Response[Problem | ReleaseApprovalPolicy]:
    """Require approvals for an app's production releases.

     Creates or replaces the policy. Each production release then waits in a canary stage until enough
    eligible people approve it. Organization owners and admins may change the policy. An app outside an
    organization always counts its owner's own approval.

    Args:
        slug (str):
        body (SetReleaseApprovalPolicyRequest): Sets an app's approval policy. Omitted fields take
            the defaults: stage 1, a 24-hour timeout, owner or admin approvers, no self-approval.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ReleaseApprovalPolicy]
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
    body: SetReleaseApprovalPolicyRequest,
) -> Problem | ReleaseApprovalPolicy | None:
    """Require approvals for an app's production releases.

     Creates or replaces the policy. Each production release then waits in a canary stage until enough
    eligible people approve it. Organization owners and admins may change the policy. An app outside an
    organization always counts its owner's own approval.

    Args:
        slug (str):
        body (SetReleaseApprovalPolicyRequest): Sets an app's approval policy. Omitted fields take
            the defaults: stage 1, a 24-hour timeout, owner or admin approvers, no self-approval.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ReleaseApprovalPolicy
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
    body: SetReleaseApprovalPolicyRequest,
) -> Response[Problem | ReleaseApprovalPolicy]:
    """Require approvals for an app's production releases.

     Creates or replaces the policy. Each production release then waits in a canary stage until enough
    eligible people approve it. Organization owners and admins may change the policy. An app outside an
    organization always counts its owner's own approval.

    Args:
        slug (str):
        body (SetReleaseApprovalPolicyRequest): Sets an app's approval policy. Omitted fields take
            the defaults: stage 1, a 24-hour timeout, owner or admin approvers, no self-approval.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ReleaseApprovalPolicy]
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
    body: SetReleaseApprovalPolicyRequest,
) -> Problem | ReleaseApprovalPolicy | None:
    """Require approvals for an app's production releases.

     Creates or replaces the policy. Each production release then waits in a canary stage until enough
    eligible people approve it. Organization owners and admins may change the policy. An app outside an
    organization always counts its owner's own approval.

    Args:
        slug (str):
        body (SetReleaseApprovalPolicyRequest): Sets an app's approval policy. Omitted fields take
            the defaults: stage 1, a 24-hour timeout, owner or admin approvers, no self-approval.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ReleaseApprovalPolicy
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
