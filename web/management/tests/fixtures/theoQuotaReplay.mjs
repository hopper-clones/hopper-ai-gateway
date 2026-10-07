import { readFileSync } from 'node:fs';

/** Synthetic HTTP replay for a browser test context; product binaries remain unchanged. */
export async function installTheoQuotaReplay(context) {
  const fixture = JSON.parse(readFileSync(new URL('./theo-quota-replay.json', import.meta.url), 'utf8'));
  await context.addInitScript(({ clock }) => {
    const NativeDate = Date;
    const referenceOffset = clock - NativeDate.now();
    class FixtureDate extends NativeDate {
      constructor(...args) { super(...(args.length ? args : [NativeDate.now() + referenceOffset])); }
      static now() { return NativeDate.now() + referenceOffset; }
    }
    globalThis.Date = FixtureDate;
  }, { clock: Date.parse(fixture.clock) });
  const observedCalls = [];
  await context.route('**/v8/management/**', async (route) => {
    const request = route.request();
    const target = new URL(request.url());
    const json = (body, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
    if (target.pathname.endsWith('/credentials') && request.method() === 'GET') return json(fixture.authFiles);
    if (target.pathname.endsWith('/credentials/download')) return json({ type: 'kimi', domain: 'kimi.com' });
    if (/reset-grants/.test(target.pathname) && request.method() === 'GET') return json({ grants: [] });
    if (!target.pathname.endsWith('/requests/api-call')) return route.continue();
    const payload = request.postDataJSON();
    const key = payload.authIndex ?? payload.auth_index;
    const upstream = new URL(payload.url);
    const record = fixture.responses[key];
    observedCalls.push({ key, method: payload.method, path: upstream.pathname });
    if (!record || payload.method !== 'GET') return json({ status_code: 403, header: {}, body: { error: 'Synthetic replay does not permit provider writes.' } });
    let body;
    if (upstream.pathname.endsWith('/profile')) body = record.profile;
    else if (upstream.pathname.endsWith('/rate-limit-reset-credits')) body = { available_count: 0, applicable_available_count: 0, credits: [] };
    else if (upstream.pathname.endsWith('/billing')) body = record.billing;
    else if (/usage|usages/.test(upstream.pathname)) body = record.usage;
    if (!body) return json({ status_code: 404, header: {}, body: { error: 'Optional replay endpoint has no retained fixture.' } });
    return json({ status_code: 200, header: {}, body, ...(key.startsWith('claude-') || key.startsWith('codex-') ? { routing_observation: { status: 'applied' } } : {}) });
  });
  return { fixture, observedCalls };
}
