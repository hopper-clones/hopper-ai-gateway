import { test, expect } from "bun:test";
import { projectCapacity } from "./capacity-reader.mjs";
test("projects every account and preserves unknown values without private identities", () => {
  const accounts = Array.from({ length: 701 }, (_, i) => ({
    accountRef: `private-${i}`,
    provider: "codex",
    alias: "private@example.com",
    email: "private@example.com",
    providerAccountId: "secret",
    meters: [{ id: "secret-meter", state: "stale", remaining: null }],
    modelAvailability: { providerAccountId: "secret" },
  }));
  const result = projectCapacity({
    snapshot: { accounts },
    composition: {
      accounts,
      mappings: [],
      coverage: "explicit-local-source-bindings",
    },
    tokens: {
      groups: { providers: [] },
      total: 10,
      input: 8,
      output: 2,
      cachedInput: 6,
      coverage: "partial",
    },
    accountTokens: {
      accounts: [
        {
          accountRef: "private-0",
          provider: "codex",
          reportedTokens: null,
          lifetimeTokens: null,
        },
      ],
      reportedTokens: null,
      coverage: {
        requestedAccounts: 701,
        reportedAccounts: 0,
        freshAccounts: 0,
      },
    },
    mode: "saved-owner-reader",
    version: "fixture",
  });
  expect(result.accounts).toHaveLength(701);
  expect(result.accounts[0].meters[0].remaining).toBeNull();
  expect(result.tokens.account.reportedTokens).toBeNull();
  expect(result.tokens.account.lifetimeTokens).toBeNull();
  expect(result.tokens.local.total).toBe(10);
  expect(result.tokens.local.cachedInput).toBe(6);
  const encoded = JSON.stringify(result);
  for (const secret of [
    "private-",
    "private@example.com",
    "secret-meter",
    "providerAccountId",
  ])
    expect(encoded).not.toContain(secret);
});
