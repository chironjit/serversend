// This public probe sends no cookies or credentials and follows no redirects.
// Treat malformed responses and network failures as unavailable, without
// displaying raw diagnostics from the network or database.
export async function checkReadiness(signal: AbortSignal): Promise<boolean> {
  const response = await fetch('/readyz', {
    signal,
    credentials: 'omit',
    redirect: 'error',
    cache: 'no-store',
    headers: { Accept: 'application/json' },
  });
  if (!response.ok) return false;
  const body: unknown = await response.json();
  return typeof body === 'object' && body !== null && 'status' in body && body.status === 'ready';
}
