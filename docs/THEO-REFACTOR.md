# Theo gateway refactor: implementation and evidence

Audit recorded at **2026-10-07T10:12:58.545646Z**. This document concerns the gateway behavior and console demonstrated around 18:04–22:01 in Theo's video. It does not claim implementation of the video's broader fleet, remote-access, hardware, agent-interface, or other application recommendations.

## Sources and provenance

- [Theo — “If you have a Claude sub, watch this”](https://www.youtube.com/watch?v=D8PikZ1KhUo), published 2026-10-02, duration 66:36. The routing discussion starts at [18:22](https://www.youtube.com/watch?v=D8PikZ1KhUo&t=1102s); transport and session/cache discussion starts at [20:38](https://www.youtube.com/watch?v=D8PikZ1KhUo&t=1238s).
- Full retained transcript, JSON (`transcript.json`, retained in private operator evidence) and full retained transcript, text (`transcript.txt`, retained in private operator evidence). The JSON contains 2,003 automatic-caption segments from 00:00 through 66:35.44. It is not a shortened extract or independently verified transcription.
- Complete organized findings (`organized-findings.json`, retained in private operator evidence). This retained inventory has 50 recommendations. Its count is not a claim that those recommendations have been implemented here.
- Original 18:04 video frame (`theo-dashboard-18m04s.png`, retained in private operator evidence). The source report records a 3840 × 2160 FFmpeg extraction from the original video, without AI reconstruction or retouching. The webcam overlay remains. It shows the CPAMC Quota Management viewport, including provider summaries, account windows, reset times, and refresh controls.
- Fresh backend source: [CLIProxyAPI upstream](https://github.com/router-for-me/CLIProxyAPI), baseline `57bde35179ecbdca176ca8923d39cc18805774f2`. No implementation was copied from the deleted earlier Hopper fork.
- Imported console source: [CLI Proxy API Management Center upstream](https://github.com/router-for-me/Cli-Proxy-API-Management-Center), revision `6abace9ffb83a9ac349464ded04bb4e7f7cb309e`. The source and its MIT license are retained under [web/management](../web/management). Hosted CI definitions were excluded from the import.

**Theo's exact private/custom fork source was unavailable.** The report says no exact public repository was identified; VibeProxy was a named starting point, not proof of fork identity. The screenshot supports the visible viewport only. Neither this implementation nor its tests claim to recover hidden pages, undisclosed code, every sidebar item, or Theo's private account configuration.

## Implemented gateway behavior

`routing.strategy: reset-first` is a first-class runtime strategy, also accepting `resetfirst` and `rf`. It is selectable in the console's visual configuration editor. Existing active configuration is not automatically rewritten, and the example keeps the upstream default `round-robin`.

For cold requests, reset-first chooses an eligible account in the configured highest available priority tier whose relevant observed quota has the soonest future reset. Provider eligibility, model support, disabled/error status, retries, pins, and normal classified cooldowns remain effective. Equal reset times and completely unknown observations use stable credential-ID ordering. Available WebSocket credentials retain the existing downstream WebSocket preference.

The strategy deliberately follows the manager's model-aware selection path rather than the cached round-robin scheduler. Each credential's resolved upstream model is retained in selection context, so model aliases and prefixes do not rank or block the wrong model. Account-level five-hour/broad weekly exhaustion and applicable model weekly exhaustion are checked before narrowing priority tiers or accepting warm session bindings.

Observed windows are interpreted from the existing provider-owned `auth.quota.signals` snapshot and its `observed_at`, including newer applicable model observations. Management projections may expose model observations as `model_quotas`; the SDK auth type serializes `model_states`.

| Provider | Relevant observed scopes |
| --- | --- |
| Claude | Shared `5h`, shared `7d`, and weekly Sonnet/Opus/Fable windows only for their matching resolved model family. The native `7d_oi` header namespace is treated as Fable-specific, consistent with the current upstream executor. |
| Codex | Primary and secondary windows, plus named model windows only when the explicit provider limit name exactly matches the resolved model. Opaque additional-limit IDs alone do not establish model identity. |

Allowed observations older than five minutes remain retained but do not rank as fresh resets. A known exhausted window with a future reset continues to block until that reset, even when stale; every applicable exhausted window must recover. A recognized exhausted window without a reset blocks only while its observation is fresh. Missing, malformed, expired, future-dated, and unsupported observations do not fabricate quota capacity or reset times. Unified/overage denial alone does not become an invented broad weekly cap; the existing provider-classified cooldown remains authoritative for such errors. These observation-based eligibility rules apply to reset-first, preserving the behavior of existing routing strategies.

With `routing.session-affinity: true`, established session/account bindings outrank changing reset order and credential priority while their credential remains eligible. Existing parent/subagent and content-prefix matching paths remain in use. Exhaustion or ordinary unavailability allows failover and rebinding. This setting is explicit; choosing reset-first alone does not silently enable affinity or change a user's active configuration.

The Codex transport change preserves an explicit Chat Completions `prompt_cache_key`, otherwise the canonical provider session UUID, otherwise the existing API-key-derived cache identity. Current upstream WebSocket transport and reconnect/session isolation are retained. Deterministic tests inspect cache headers and upstream-reported cache usage; they do not establish a real provider's hit rate or guarantee a cache hit after account failover.

## Console and packaging

The maintained upstream console remains the full application: account/auth files, OAuth, providers, configuration, logs, system information, dashboard, and conditional plugin pages. The quota page now leads with provider summaries and an account ledger, including relevant windows, reset times, masked account identities, and refresh actions. Existing card/timeline paths remain available. Unknown quota stays visibly unknown; aggregate percentages summarize comparable account allowance windows, not interchangeable tokens or guaranteed provider capacity.

Manual sign-in and remembered-session restoration preserve the requested internal route, query, and fragment. A sign-in without a requested destination opens `/quota`. External protocol-relative destinations and a `/login` redirect are rejected. This is an authenticated management-key flow; it is not a passwordless replacement or a bypass of the existing remote-access policy.

The console is packaged as a single production HTML document, compressed and embedded in the Go binary. [The packaging script](../web/management/scripts/package-console.mjs) records source and HTML SHA-256 digests and HTML byte count in [the manifest](../internal/managementasset/console/manifest.json). The gateway serves that embedded artifact at the existing management-panel URL. The automatic upstream management-asset downloader is disabled for this gateway, so it cannot silently replace the fork-owned console. Direct SDK downloader utilities remain available.

To rebuild the console before a Go build:

```sh
cd web/management
bun install --frozen-lockfile
bun run build
bun run package:console
cd ../..
go build -o cli-proxy-api ./cmd/server
```

Native successful quota refresh responses on the v8 management API can update routing through `Manager.ObserveQuotaHeaders`. The method updates observations in memory under credential mutation and manager locks, not execution success counters, cooldown state, credential persistence, or authentication status. Empty or older snapshots leave the latest observation intact; provider mismatch and future timestamps are rejected. Failed/incomplete provider payloads and display-only plugin bucket labels must not become guessed routing limits. Existing deprecated v0 endpoint behavior is retained.

## Validation and limits

Implemented and deterministically tested are separate from configured, deployed, and live-provider-qualified. The routing owner passed auth/SDK/config suites, alias/priority/exhaustion/affinity cases, concurrent refresh/select race tests, and a server compile check. Transport qualification uses real local HTTP/WebSocket upstream peers to inspect cache identity, account isolation, continuation, reconnect, fallback, and usage signals. A loopback peer is transport evidence, not a live Claude/Codex account.

Independent final full-tree validation passed: `go test ./...`, started `2026-10-07T10:11:54.013334Z`, finished `2026-10-07T10:12:04.420734Z`, exit 0. The checked working tree was based on `62db51e403d9d81a40fe37e67e16262ed4bbfd9a` plus the pending console packaging and media-fixture changes. Lossless final log (`full-go-test-qualified.log`, retained in private operator evidence); machine result (`full-go-test-qualified-result.json`, retained in private operator evidence).

The first full-tree run (`full-go-test.log`, retained in private operator evidence) began at `2026-10-07T10:05:26.182094Z` and ended at `2026-10-07T10:06:00.301210Z`, exit 1. It captured a concurrently corrected missing import and a DataChannel timeout in the pre-existing WebRTC media test. That test also failed in isolation. A read-only Go overlay using loopback-only peers passed the same complete audio/data assertions in 0.01 seconds, isolating its dependency on this host's LAN/VPN/bridge ICE candidates. The authorized correction restricts only this local test fixture's four peers to IPv4 UDP loopback; production relay construction is unchanged. Its race qualification (`media-loopback-race-result.json`, retained in private operator evidence) passed at `2026-10-07T10:11:32.421927Z`. No assertion or failing test was skipped.

The embedded gzip's decompressed bytes matched the recorded HTML digest during the independent audit. Subsequent source edits require rebuilding and repackaging the console so its source digest also matches. Release packaging and any local browser evidence are recorded separately by the console owner.

No claim is made here that the new binary is deployed on the owner's persistent gateway, that routing settings are enabled there, that multiple live provider accounts have been exercised, or that live subscription reset/latency/cache economics reproduce Theo's setup. Those states require their own exact runtime and provider evidence. Local preview/browser checks, when recorded separately by the console owner, are UI evidence only. No external hosting or publication follows from this work.
