from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.binding_release_policy import BindingReleasePolicy
from ...models.problem import Problem
from ...models.set_queue_binding_release_policy_request import SetQueueBindingReleasePolicyRequest
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: SetQueueBindingReleasePolicyRequest,
    scope: str | Unset = "default",
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    params: dict[str, Any] = {}

    params["scope"] = scope

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/bindings/release-policy-with-queues".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> BindingReleasePolicy | Problem | None:
    if response.status_code == 200:
        response_200 = BindingReleasePolicy.from_dict(response.json())

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
) -> Response[BindingReleasePolicy | Problem]:
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
    body: SetQueueBindingReleasePolicyRequest,
    scope: str | Unset = "default",
) -> Response[BindingReleasePolicy | Problem]:
    """Replace release policy with explicit per-binding queue readiness requirements.

     Requires deploy:write or admin and completed MFA. queue_requirements is
    explicit, including an empty object to clear it. Names select one shared
    or selected-environment push binding; consumer_readiness requires a
    confirmed queue environment and an enabled, active, healthy scheduler
    consumer with a poll at most thirty seconds old. Missing, ambiguous,
    disabled, external pull, unobserved or
    stale consumers block. Connectivity and message delivery remain
    unverified; passed readiness policies retain partial coverage. Unlisted
    queues retain strict unsupported coverage. Existing storage requirements
    must be explicitly retained or cleared. Policy and queue facts, and poll
    expiry, are checked at the traffic write. This call sends no messages and
    performs no probes. Older servers return 404 before mutation.

    Args:
        slug (str):
        scope (str | Unset):  Default: 'default'.
        body (SetQueueBindingReleasePolicyRequest): Revision-guarded release policy replacement
            with explicit per-binding queue readiness requirements and optional storage requirements.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BindingReleasePolicy | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        scope=scope,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetQueueBindingReleasePolicyRequest,
    scope: str | Unset = "default",
) -> BindingReleasePolicy | Problem | None:
    """Replace release policy with explicit per-binding queue readiness requirements.

     Requires deploy:write or admin and completed MFA. queue_requirements is
    explicit, including an empty object to clear it. Names select one shared
    or selected-environment push binding; consumer_readiness requires a
    confirmed queue environment and an enabled, active, healthy scheduler
    consumer with a poll at most thirty seconds old. Missing, ambiguous,
    disabled, external pull, unobserved or
    stale consumers block. Connectivity and message delivery remain
    unverified; passed readiness policies retain partial coverage. Unlisted
    queues retain strict unsupported coverage. Existing storage requirements
    must be explicitly retained or cleared. Policy and queue facts, and poll
    expiry, are checked at the traffic write. This call sends no messages and
    performs no probes. Older servers return 404 before mutation.

    Args:
        slug (str):
        scope (str | Unset):  Default: 'default'.
        body (SetQueueBindingReleasePolicyRequest): Revision-guarded release policy replacement
            with explicit per-binding queue readiness requirements and optional storage requirements.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BindingReleasePolicy | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        scope=scope,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetQueueBindingReleasePolicyRequest,
    scope: str | Unset = "default",
) -> Response[BindingReleasePolicy | Problem]:
    """Replace release policy with explicit per-binding queue readiness requirements.

     Requires deploy:write or admin and completed MFA. queue_requirements is
    explicit, including an empty object to clear it. Names select one shared
    or selected-environment push binding; consumer_readiness requires a
    confirmed queue environment and an enabled, active, healthy scheduler
    consumer with a poll at most thirty seconds old. Missing, ambiguous,
    disabled, external pull, unobserved or
    stale consumers block. Connectivity and message delivery remain
    unverified; passed readiness policies retain partial coverage. Unlisted
    queues retain strict unsupported coverage. Existing storage requirements
    must be explicitly retained or cleared. Policy and queue facts, and poll
    expiry, are checked at the traffic write. This call sends no messages and
    performs no probes. Older servers return 404 before mutation.

    Args:
        slug (str):
        scope (str | Unset):  Default: 'default'.
        body (SetQueueBindingReleasePolicyRequest): Revision-guarded release policy replacement
            with explicit per-binding queue readiness requirements and optional storage requirements.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BindingReleasePolicy | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        scope=scope,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetQueueBindingReleasePolicyRequest,
    scope: str | Unset = "default",
) -> BindingReleasePolicy | Problem | None:
    """Replace release policy with explicit per-binding queue readiness requirements.

     Requires deploy:write or admin and completed MFA. queue_requirements is
    explicit, including an empty object to clear it. Names select one shared
    or selected-environment push binding; consumer_readiness requires a
    confirmed queue environment and an enabled, active, healthy scheduler
    consumer with a poll at most thirty seconds old. Missing, ambiguous,
    disabled, external pull, unobserved or
    stale consumers block. Connectivity and message delivery remain
    unverified; passed readiness policies retain partial coverage. Unlisted
    queues retain strict unsupported coverage. Existing storage requirements
    must be explicitly retained or cleared. Policy and queue facts, and poll
    expiry, are checked at the traffic write. This call sends no messages and
    performs no probes. Older servers return 404 before mutation.

    Args:
        slug (str):
        scope (str | Unset):  Default: 'default'.
        body (SetQueueBindingReleasePolicyRequest): Revision-guarded release policy replacement
            with explicit per-binding queue readiness requirements and optional storage requirements.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BindingReleasePolicy | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            scope=scope,
        )
    ).parsed
