/** Eligible transient failures; lifecycle reconciliation reads receipts without replaying writes. */
export function statusReadRetryable(error: unknown): boolean {
 if(error && typeof error==='object' && 'status' in error) return [502,503,504].includes(Number(error.status));
 if(error instanceof TypeError) return /fetch failed|failed to fetch|network|connection|socket/i.test(error.message);
 if(error && typeof error==='object' && 'code' in error) return ['ECONNRESET','ECONNREFUSED','EPIPE','ETIMEDOUT','UND_ERR_SOCKET','UND_ERR_CONNECT_TIMEOUT','UND_ERR_HEADERS_TIMEOUT','UND_ERR_BODY_TIMEOUT'].includes(String(error.code));
 return false;
}
export function statusReadRetryDelay(attempt: number): number {
 const cap=Math.min(250*2**(attempt-1),2000);return Math.floor(cap/2+Math.random()*(cap/2+1));
}
