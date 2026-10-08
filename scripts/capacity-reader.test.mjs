import { test, expect } from "bun:test";
import {
  projectCapacity,
  projectCodexAccounts,
  pickModel,
} from "./capacity-reader.mjs";
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
test("lists Codex accounts with the selector's private connection or its refusal", async () => {
  const catalog = (models) => ({ modelAvailability: { models } });
  const snapshot = {
    accounts: [
      { accountRef: "c-1", provider: "claude" },
      { accountRef: "x-1", provider: "codex", ...catalog([{ model: "m-1", reasoningEfforts: ["low", "medium"] }]) },
      { accountRef: "x-2", provider: "codex", ...catalog([{ model: "m-2", reasoningEfforts: ["high"] }]) },
      { accountRef: "x-3", provider: "codex" },
    ],
  };
  const asked = [];
  const result = await projectCodexAccounts({
    snapshot,
    resolveCodex: async (input) => {
      asked.push(input);
      if (input.accountRef === "x-2")
        throw Object.assign(Error("CAPACITY_CONNECTION_NOT_CURRENT"), {
          code: "CAPACITY_CONNECTION_NOT_CURRENT",
        });
      return {
        selection: {},
        executable: "/bin/codex",
        subscriptionAccount: {
          home: "/private/home-1",
          accountRef: input.accountRef,
          expectedEmail: "one@example.com",
          providerAccountId: "acct-1",
        },
      };
    },
  });
  expect(result.schemaVersion).toBe("hopper.gateway-capacity-codex.v1");
  expect(asked).toEqual([
    { accountRef: "x-1", model: "m-1", reasoningEffort: "medium" },
    { accountRef: "x-2", model: "m-2", reasoningEffort: "high" },
  ]);
  expect(result.accounts.map((a) => [a.label, a.refused])).toEqual([
    ["codex 1", null],
    ["codex 2", "CAPACITY_CONNECTION_NOT_CURRENT"],
    ["codex 3", "CAPACITY_MODEL_CATALOG_REQUIRED"],
  ]);
  expect(result.accounts[0].account).toEqual({
    accountRef: "x-1",
    home: "/private/home-1",
    expectedEmail: "one@example.com",
    providerAccountId: "acct-1",
  });
  expect(result.accounts[0].id).toHaveLength(24);
  expect(JSON.stringify(result)).not.toContain("/bin/codex");
  expect(pickModel({})).toBeNull();
});
