# Hopper AI Capacity bridge

The authenticated local management endpoint `GET /v8/management/capacity/snapshot` reads Hopper AI Capacity's public owner surfaces. Capacity owns observations; Gateway never reads its private database, discovers credentials, polls providers, enrolls accounts or routes requests from this inventory.

Explicitly configure `HOPPER_AI_CAPACITY_READER` with the absolute path to `scripts/capacity-reader.mjs`, `HOPPER_AI_CAPACITY_STATE` with the selected owner state directory, and `HOPPER_AI_CAPACITY_PACKAGE` with an extracted exact public `@hopper/ai-capacity` archive. Record and verify that archive's SHA-256 in the installation process. `HOPPER_AI_CAPACITY_BUN` optionally selects the Bun executable; otherwise Gateway finds Bun on PATH. Operator paths and session material remain outside version control.

The adapter first uses the selected local-session HTTP endpoint with its private bearer token. If that owner child is unavailable, the archive's exported `createCapacityReader` reads saved observations, with account discovery disabled and no collector started. The actual Gateway socket peer must be loopback; forwarded headers do not establish local access. Missing configuration or owner failures return explicit errors rather than empty inventory.

`hopper.gateway-capacity.v1` returns all accounts with opaque hashed IDs and provider/ordinal labels, plan, observation freshness, meters, reset credits and usage credits. It excludes email, aliases, source paths, provider account IDs, credentials and model availability identity records. Saved stale or unavailable meters retain their states.

`tokens.local` preserves total/input/output/cachedInput/count, provider groups and coverage. Cached input is already included in input; never add it again. `tokens.account` preserves provider-reported dated totals, nullable lifetime totals, coverage counts and sanitized account rows. Lifetime totals include only reporting accounts, with `lifetimeReportedAccounts` documenting that subset. Local measurements and provider-account reports are separate accounting bases and must never be added. Unsupported providers, missing dates, charges, cost estimates and Gateway attribution remain unknown or explicitly unavailable. All provider pages are read; invalid continuation fails explicitly.

Run `bun test scripts/capacity-reader.test.mjs` and `go test ./internal/api/handlers/management -run TestCapacityBridge` for the bridge checks. This integration does not imply fresh provider qualification or Capacity release admission.

The quota screen uses the same native console. Select **AI Capacity accounts** or **Gateway credentials**. An empty Gateway defaults to the available Capacity inventory; account data never becomes a credential file. All providers remain visible, including providers without a Gateway quota adapter. Saved or expired observations show unknown current allowance and retain their reported percentage as history. Credits show known expiration or an explicit unknown expiration. Recorded local token totals and provider account reports are separate; lifetime coverage is shown with its reporting-account count.

## Existing provider sessions

Capacity's selected official-client sessions can serve identity and usage reads without another sign-in when those sessions remain valid. The current subscription-reader public surface does not export credentials or offer arbitrary model requests. Its fixed connection-test message does not establish a general Gateway executor. Provider account references are not tokens.

Reusing a provider login for Gateway routing needs an owner executor transport or an approved Account/Secrets binding for that executor. The existing governed Account grant and Secrets executor-resolution surfaces require exact signed context; they cannot be replaced by scanning client stores or copying browser sessions. This bridge establishes neither that grant nor live routed-model qualification.

## Local console launch

Launch the native server with `--open-console` on a literal loopback listener and a configured management key. The browser opens directly to the quota screen with a single-use capability, consumes and erases it, and exchanges it for an HttpOnly SameSite=Strict cookie. Reload restores that local session without copying the management key into browser storage. A bare URL in a fresh browser has no launch capability and still needs authorized access. The local session requires the actual loopback peer, exact Host, a custom request header and same-origin mutation requests; remote key authentication remains intact. Logout, process restart, expiration and management policy changes revoke the session.

The integrated build passed frontend tests, lint, TypeScript, full Go tests and the native build. Private browser acceptance exercised real owner inventory, automatic launch, reload without a key, provider filtering, source switching, refresh and mobile navigation. Real account identities, usage statistics, session material and those captures stay in private operator evidence.
