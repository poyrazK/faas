(() => {
  'use strict';
  const button = document.getElementById('unsubscribe-confirm'), status = document.getElementById('unsubscribe-status');
  let token = '', revision = 0;
  const open = () => {
    revision++;
    const url = new URL(location.href), fragment = new URLSearchParams(url.hash.slice(1));
    const queryKeys = [...url.searchParams.keys()], fragmentKeys = [...fragment.keys()];
    token = queryKeys.length === 0 && fragmentKeys.length === 1 && fragmentKeys[0] === 'token' ? fragment.get('token') : fragmentKeys.length === 0 && queryKeys.length === 1 && queryKeys[0] === 'token' ? url.searchParams.get('token') : '';
    // Remove the capability before any request or user navigation. Keep it only
    // in memory so an uncertain response can be retried with the same link.
    history.replaceState(null, '', url.pathname);
    button.disabled = true; button.textContent = 'Turn off my notifications'; status.textContent = '';
    if (!/^[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]$/.test(token || '')) {
      token = ''; status.textContent = 'This link is incomplete. Open the unsubscribe link in a recent email, or sign in to manage your preferences.'; return;
    }
    button.disabled = false;
  };
  button.onclick = async () => {
    if (button.disabled || !token) return;
    const at = revision, submitted = token;
    button.disabled = true; status.textContent = 'Turning off your notifications…';
    const controller = new AbortController(), timer = setTimeout(() => controller.abort(), Number(document.body.dataset.requestTimeoutMs));
    try {
      const response = await fetch('/v1/product-control/email/unsubscribe', { method: 'POST', credentials: 'omit', cache: 'no-store', redirect: 'error', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ token: submitted }), signal: controller.signal });
      const result = await response.json();
      if (at !== revision) return;
      if (!response.ok) {
        if (response.status === 400 || response.status === 403 || response.status === 410) { token = ''; status.textContent = response.status === 410 ? 'This link has expired or newer preferences were saved. Use a recent email or sign in to manage your current preferences.' : 'This link could not be confirmed. Use a recent email or sign in to manage your preferences.'; return; }
        throw new Error('Unsubscribe could not be checked.');
      }
      if (!/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/.test(result?.receipt_id || '') || typeof result?.unsubscribed_at !== 'string' || !Number.isFinite(new Date(result.unsubscribed_at).getTime()) || typeof result.current !== 'boolean') throw new Error('Unsubscribe acknowledgement could not be checked.');
      token = '';
      button.textContent = result.current ? 'Unsubscribe confirmed' : 'Previous unsubscribe checked';
      status.textContent = result.current ? `Email notifications are off for you in this workspace. Confirmation: ${result.receipt_id}. An email already being sent may still arrive.` : `This unsubscribe was recorded previously. Newer preferences have been saved since then and were left unchanged. Sign in to check your current choices. Confirmation: ${result.receipt_id}.`;
    } catch {
      if (at !== revision) return;
      button.disabled = false; button.textContent = 'Check or repeat unsubscribe'; status.textContent = 'The response could not be checked. Your unsubscribe may have completed. Check or repeat the same unsubscribe to confirm.';
    } finally { clearTimeout(timer); }
  };
  addEventListener('hashchange', open);
  open();
})();
