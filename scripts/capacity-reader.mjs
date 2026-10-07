import { readFile } from "node:fs/promises";
import { resolve, isAbsolute, join } from "node:path";
import { pathToFileURL } from "node:url";
import { createHash } from "node:crypto";
const n = (v) => (Number.isFinite(v) ? v : null),
  s = (v) => (typeof v === "string" ? v : null);
const hash = (v) =>
  createHash("sha256").update(String(v)).digest("hex").slice(0, 24);
export function projectCapacity({
  snapshot,
  composition,
  tokens,
  accountTokens,
  mode,
  version,
}) {
  if (
    !Array.isArray(snapshot?.accounts) ||
    !Array.isArray(composition?.accounts) ||
    !Array.isArray(tokens?.groups?.providers) ||
    !Array.isArray(accountTokens?.accounts)
  )
    throw Error("CAPACITY_INVALID_RESPONSE");
  const ordinals = {},
    current = new Map(composition.accounts.map((a) => [a.accountRef, a]));
  const accounts = snapshot.accounts.map((a) => ({
    id: hash(a.accountRef),
    provider: s(a.provider),
    label: `${a.provider} ${(ordinals[a.provider] = (ordinals[a.provider] ?? 0) + 1)}`,
    plan: s(a.plan),
    observedAt: s(a.observedAt),
    freshness: s(current.get(a.accountRef)?.freshness ?? a.freshness),
    meters: (a.meters ?? []).map((m) => ({
      id: hash(`${a.accountRef}:${m.id}`),
      label: s(m.label),
      model: s(m.model),
      remaining: n(m.remaining),
      state: s(m.state),
      resetsAt: s(m.resetsAt),
      kind: s(m.kind),
    })),
    resetCredits: Array.isArray(a.resetCredits)
      ? a.resetCredits.map((c) => ({
          id: hash(`${a.accountRef}:${c.id}`),
          expiresAt: s(c.expiresAt),
          expiryKnown:
            typeof c.expiryKnown === "boolean" ? c.expiryKnown : null,
          status: s(c.status),
        }))
      : null,
    resetCreditsAvailable: n(a.resetCreditsAvailable),
    usageCredits: Array.isArray(a.usageCredits)
      ? a.usageCredits.map((c) => ({
          limitId: hash(`${a.accountRef}:${c.limitId}`),
          label: s(c.label),
          hasCredits: typeof c.hasCredits === "boolean" ? c.hasCredits : null,
          unlimited: typeof c.unlimited === "boolean" ? c.unlimited : null,
          balance: s(c.balance),
        }))
      : null,
  }));
  const lifetime = accountTokens.accounts.filter((a) =>
    Number.isFinite(a.lifetimeTokens),
  );
  return {
    schemaVersion: "hopper.gateway-capacity.v1",
    readAt: new Date().toISOString(),
    source: { product: "Hopper AI Capacity", mode, version },
    accounts,
    tokens: {
      local: {
        from: s(tokens.from),
        to: s(tokens.to),
        total: n(tokens.total),
        input: n(tokens.input),
        output: n(tokens.output),
        cachedInput: n(tokens.cachedInput),
        count: n(tokens.count),
        coverage: s(tokens.coverage),
        legacyDetailUnavailable: tokens.legacyDetailUnavailable === true,
        partialBoundary: tokens.partialBoundary === true,
        hasMore: false,
        providers: tokens.groups.providers.map((p) => ({
          key: s(p.key),
          count: n(p.count),
          input: n(p.input),
          output: n(p.output),
          cachedInput: n(p.cachedInput),
          total: n(p.total),
        })),
        measuredCharges: s(tokens.capabilities?.measuredCharges),
        costEstimate: s(tokens.capabilities?.costEstimate),
        gatewayRequests: s(tokens.capabilities?.gatewayRequests),
        multiDevice: s(tokens.capabilities?.multiDevice),
      },
      account: {
        from: s(accountTokens.from),
        to: s(accountTokens.to),
        reportedTokens: n(accountTokens.reportedTokens),
        lifetimeTokens: lifetime.length
          ? lifetime.reduce((sum, a) => sum + a.lifetimeTokens, 0)
          : null,
        lifetimeReportedAccounts: lifetime.length,
        coverage: {
          requestedAccounts: n(accountTokens.coverage?.requestedAccounts),
          reportedAccounts: n(accountTokens.coverage?.reportedAccounts),
          freshAccounts: n(accountTokens.coverage?.freshAccounts),
        },
        accounts: accountTokens.accounts.map((a) => ({
          id: hash(a.accountRef),
          provider: s(a.provider),
          observedAt: s(a.observedAt),
          state: s(a.state),
          reportedTokens: n(a.reportedTokens),
          lifetimeTokens: n(a.lifetimeTokens),
          coverage: s(a.coverage),
          cacheSemantics: s(a.cacheSemantics),
        })),
        accounting:
          "Provider account reports only; never add local call totals. Unsupported providers and missing dates remain unknown.",
      },
    },
    attribution: {
      mappings: composition.mappings?.length ?? 0,
      coverage: s(composition.coverage),
      knowledge: s(composition.attributionKnowledge),
    },
  };
}
export async function readCapacity(env = process.env) {
  const state = env.HOPPER_AI_CAPACITY_STATE,
    root = env.HOPPER_AI_CAPACITY_PACKAGE;
  if (!state || !root || !isAbsolute(state) || !isAbsolute(root))
    throw Error("CAPACITY_UNCONFIGURED");
  const manifest = JSON.parse(
    await readFile(join(root, "package.json"), "utf8"),
  );
  if (
    manifest.name !== "@hopper/ai-capacity" ||
    !manifest.exports?.["./reader"]
  )
    throw Error("CAPACITY_INVALID_PACKAGE");
  let reader, invoke, mode;
  try {
    let session;
    try {
      session = JSON.parse(
        await readFile(join(state, "local-session.json"), "utf8"),
      );
    } catch {}
    if (session?.url) {
      const origin = new URL(session.url);
      if (
        !["127.0.0.1", "localhost", "[::1]"].includes(origin.hostname) ||
        !["http:", "https:"].includes(origin.protocol) ||
        origin.username ||
        origin.password
      )
        throw Error("CAPACITY_INVALID_SESSION");
      const headers = session.token
          ? { authorization: `Bearer ${session.token}` }
          : {},
        request = async (name, input = {}) => {
          const url = new URL(`/v1/capacity/${name}`, origin);
          for (const [k, v] of Object.entries(input))
            url.searchParams.set(k, String(v));
          const res = await fetch(url, { headers, redirect: "error" });
          if (!res.ok) throw Error("CAPACITY_OWNER_UNAVAILABLE");
          return res.json();
        };
      try {
        await request("status");
        invoke = request;
        mode = "owner-http";
      } catch {}
    }
    if (!invoke) {
      process.env.HOPPER_CAPACITY_ACCOUNT_DISCOVERY = "off";
      const exp = manifest.exports["./reader"],
        entry = resolve(root, typeof exp === "string" ? exp : exp.import);
      if (!entry.startsWith(resolve(root) + "/"))
        throw Error("CAPACITY_INVALID_PACKAGE");
      const { createCapacityReader } = await import(pathToFileURL(entry).href);
      reader = await createCapacityReader(state);
      invoke = (name, input) => reader.invoke(name, input);
      mode = "saved-owner-reader";
    }
    const tomorrow = new Date();
    tomorrow.setUTCDate(tomorrow.getUTCDate() + 1);
    const to = tomorrow.toISOString().slice(0, 10);
    const snapshot = await invoke("snapshot", { reveal: false }),
      composition = await invoke("composition");
    await invoke("status");
    const tokens = await invoke("tokens", {
      from: "1970-01-01",
      to,
      group: "providers",
      seriesRange: "recorded",
    });
    let page = tokens;
    const seen = new Set();
    while (page.hasMore) {
      if (!page.nextCursor || seen.has(page.nextCursor))
        throw Error("CAPACITY_INCOMPLETE_HISTORY");
      seen.add(page.nextCursor);
      page = await invoke("tokens", {
        from: tokens.from,
        to: tokens.to,
        group: "providers",
        seriesRange: "recorded",
        cursor: page.nextCursor,
      });
      tokens.groups.providers.push(...page.groups.providers);
    }
    tokens.hasMore = false;
    const accountTokens = await invoke("account-tokens", {
      from: "1970-01-01",
      to,
    });
    return projectCapacity({
      snapshot,
      composition,
      tokens,
      accountTokens,
      mode,
      version: manifest.version,
    });
  } finally {
    if (reader) await reader.close();
  }
}
if (import.meta.main) {
  try {
    console.log(JSON.stringify(await readCapacity()));
    process.exit(0);
  } catch (e) {
    console.error(
      JSON.stringify({
        code: [
          "CAPACITY_UNCONFIGURED",
          "CAPACITY_INVALID_PACKAGE",
          "CAPACITY_INVALID_SESSION",
          "CAPACITY_INCOMPLETE_HISTORY",
        ].includes(e.message)
          ? e.message
          : "CAPACITY_OWNER_UNAVAILABLE",
      }),
    );
    process.exit(1);
  }
}
