/** The launcher capability is consumed once, before the hash router reads the URL. */
export function consumeLocalSessionFragment(
  location: Pick<Location, 'hash' | 'pathname' | 'search'>,
  replace: (url: string) => void
): string | null {
  const hash = location.hash.replace(/^#/, '');
  const separator = hash.indexOf('?');
  if (separator < 0) return null;
  const params = new URLSearchParams(hash.slice(separator + 1));
  const capability = params.get('local-session');
  if (capability === null) return null;
  params.delete('local-session');
  const query = params.toString();
  replace(
    `${location.pathname}${location.search}#${hash.slice(0, separator)}${query ? `?${query}` : ''}`
  );
  return capability;
}

let launchCapability: string | null =
  typeof window === 'undefined'
    ? null
    : consumeLocalSessionFragment(window.location, (url) =>
        window.history.replaceState(null, '', url)
      );

export function takeLocalSessionCapability(): string | null {
  const value = launchCapability;
  launchCapability = null;
  return value;
}

export function isLocalSessionOrigin(apiBase: string, origin: string): boolean {
  try {
    const url = new URL(apiBase);
    return url.origin === origin && ['127.0.0.1', '[::1]'].includes(url.hostname);
  } catch {
    return false;
  }
}
