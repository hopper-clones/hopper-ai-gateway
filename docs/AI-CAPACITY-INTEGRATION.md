# Hopper AI Capacity bridge

The authenticated local management endpoint `GET /v8/management/capacity/snapshot` reads Hopper AI Capacity's public owner surfaces. Capacity owns observations; Gateway never reads its private database, discovers credentials, polls providers, enrolls accounts or routes requests from this inventory. Routing through Capacity's Codex logins is a separate opt-in source, described in [Using Capacity's Codex logins](#using-capacitys-codex-logins).

Explicitly configure `HOPPER_AI_CAPACITY_READER` with the absolute path to `scripts/capacity-reader.mjs`, `HOPPER_AI_CAPACITY_STATE` with the selected owner state directory, and `HOPPER_AI_CAPACITY_PACKAGE` with an extracted exact public `@hopper/ai-capacity` archive. Record and verify that archive's SHA-256 in the installation process. `HOPPER_AI_CAPACITY_BUN` optionally selects the Bun executable; otherwise Gateway finds Bun on PATH. Operator paths and session material remain outside version control.

The adapter first uses the selected local-session HTTP endpoint with its private bearer token. If that owner child is unavailable, the archive's exported `createCapacityReader` reads saved observations, with account discovery disabled and no collector started. The actual Gateway socket peer must be loopback; forwarded headers do not establish local access. Missing configuration or owner failures return explicit errors rather than empty inventory.

`hopper.gateway-capacity.v1` returns all accounts with opaque hashed IDs and provider/ordinal labels, plan, observation freshness, meters, reset credits and usage credits. It excludes email, aliases, source paths, provider account IDs, credentials and model availability identity records. Saved stale or unavailable meters retain their states.

`tokens.local` preserves total/input/output/cachedInput/count, provider groups and coverage. Cached input is already included in input; never add it again. `tokens.account` preserves provider-reported dated totals, nullable lifetime totals, coverage counts and sanitized account rows. Lifetime totals include only reporting accounts, with `lifetimeReportedAccounts` documenting that subset. Local measurements and provider-account reports are separate accounting bases and must never be added. Unsupported providers, missing dates, charges, cost estimates and Gateway attribution remain unknown or explicitly unavailable. All provider pages are read; invalid continuation fails explicitly.

Run `bun test scripts/capacity-reader.test.mjs` and `go test ./internal/api/handlers/management -run TestCapacityBridge` for the bridge checks. This integration does not imply fresh provider qualification or Capacity release admission.

The quota screen uses the same native console. Select **AI Capacity accounts** or **Gateway credentials**. An empty Gateway defaults to the available Capacity inventory; account data never becomes a credential file. All providers remain visible, including providers without a Gateway quota adapter. Saved or expired observations show unknown current allowance and retain their reported percentage as history. Credits show known expiration or an explicit unknown expiration. Recorded local token totals and provider account reports are separate; lifetime coverage is shown with its reporting-account count.

## Existing provider sessions

Capacity's selected official-client sessions serve identity and usage reads without another sign-in while those sessions remain valid. Provider account references are not tokens. Codex logins can also serve Gateway requests in place, as below; other providers have no such source.

## Using Capacity's Codex logins

With `credentials.capacity-codex.enabled: true`, Gateway registers one Codex credential for each Codex account Capacity tracks, using that account's existing official Codex login. The owner does not sign in again, and no token is copied into Gateway's auth directory or configuration.

What it reads. On start and every `refresh-seconds` (default 300), Gateway runs the same reader as the bridge in `codex-accounts` mode. The reader takes the Codex accounts from Capacity's snapshot and asks the package's public `./subscription-selection` export (`createCapacitySubscriptionSelector` over `HOPPER_AI_CAPACITY_STATE`) to resolve each one, using the first model in that account's own observed catalog that lists a reasoning effort (preferring `medium`). An account the selector refuses (for example `CAPACITY_CONNECTION_NOT_CURRENT`) is skipped with its code logged and is unregistered if it was registered. A resolved account's private connection fields (Capacity account reference, Codex home, expected email, provider account ID) go to the Gateway process on the reader's stdout only and are never logged or served. The package must be an archive that exports `./subscription-selection` (0.4.20 or later). The selector only reads Capacity's database.

Which logins are used. Gateway reads `<home>/auth.json`, the official Codex login file. The account is registered only when that file exists and its `id_token` email and ChatGPT account ID match the expected email and provider account ID; otherwise it is skipped with a logged reason (`CAPACITY_CODEX_IDENTITY_MISMATCH`). The registered credential is runtime-only: it holds the email and account ID for routing and the usage feed, and reads tokens from the file.

When tokens are read and refreshed. Before each request Gateway re-reads the file if its modification time or size changed, so a login the Codex app refreshed is picked up at once. A token that is not expired is never refreshed, and the background refresh loop leaves these credentials alone. When the access token has expired, Gateway first re-reads the file; only if it is still expired does it refresh with the Codex client's existing refresh call and write the result back to the same `auth.json` in the official format: every other key and the key order are kept, `tokens` and `last_refresh` are updated, and the write is atomic (temporary file in the same directory, then rename) with the file's mode (0600). If the refresh fails, the credential is marked unavailable with `CAPACITY_CODEX_REFRESH_FAILED`, requests go to the other credentials, and the refresh is not retried until the file changes. An upstream 401 on a token that is not expired is not refreshed either: Gateway re-reads the file and otherwise reports `CAPACITY_CODEX_TOKEN_REJECTED`.

Status. `GET /v8/management/credentials` lists these credentials with `source: "capacity-codex"`, `capacity_account` (`id`, the Capacity account hash the bridge also uses, and `label`, such as `codex 2`), `state` (`ready` or `unavailable`) and `state_reason`. It never includes a token or the login file location. The bundled console shows them in its credentials list.

How to enable. Configure the reader environment as for the bridge, with a package archive that exports `./subscription-selection`, then set:

```yaml
credentials:
  capacity-codex:
    enabled: true
    refresh-seconds: 300
```

Changing it requires a restart. The reader child process has a 20 s deadline, like the bridge. Run `go test ./internal/capacitycodex` and `bun test scripts/capacity-reader.test.mjs` for the source checks.

## Local console launch

Launch the native server with `--open-console` on a literal loopback listener and a configured management key. The browser opens directly to the quota screen with a single-use capability, consumes and erases it, and exchanges it for an HttpOnly SameSite=Strict cookie. Reload restores that local session without copying the management key into browser storage. A bare URL in a fresh browser has no launch capability and still needs authorized access. The local session requires the actual loopback peer, exact Host, a custom request header and same-origin mutation requests; remote key authentication remains intact. Logout, process restart, expiration and management policy changes revoke the session.

The integrated build passed frontend tests, lint, TypeScript, full Go tests and the native build. Private browser acceptance exercised real owner inventory, automatic launch, reload without a key, provider filtering, source switching, refresh and mobile navigation. Real account identities, usage statistics, session material and those captures stay in private operator evidence.
