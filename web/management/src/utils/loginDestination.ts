type RedirectState = {
  from?: { pathname?: string; search?: string; hash?: string };
};

/** Keep the requested console route across manual sign-in and session restoration. */
export function loginDestination(state: unknown): string {
  const from = (state as RedirectState | null)?.from;
  const pathname = from?.pathname;
  if (
    !pathname ||
    !pathname.startsWith('/') ||
    pathname.startsWith('//') ||
    pathname === '/login'
  ) {
    return '/quota';
  }
  return `${pathname}${from?.search ?? ''}${from?.hash ?? ''}`;
}
