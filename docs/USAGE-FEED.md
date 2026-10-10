# Usage feed: v2 usage, v1 quota

The gateway is the only writer of the fact "a request happened". It records that
fact in an append-only JSONL feed that AI Capacity pulls over the loopback
management API. This feed covers requests routed through this gateway. Local
client history and provider account reports have separate coverage; this feed
does not prove their requests were observed.

## Files

- Directory: `usage-feed.dir` (default `usage-feed/` beside the config file).
- One file per UTC hour: `usage-feed/<YYYY-MM-DDTHH>Z.jsonl`, one JSON object per line.
- Events are queued on a buffered channel and written by one goroutine every 250 ms
  or every 500 events, with one `fsync` per batch. A request never waits on the
  feed; if the queue is full the event is dropped, counted and warned.
- `usage-feed/cursors.json` holds each consumer's acked cursor (atomic rename).
- Retention: hourly files older than 30 days are deleted only when every consumer's
  acked cursor is past them. With no consumer acked at all nothing is deleted. One
  stalled consumer therefore holds retention for everyone; drop it with
  `DELETE /v8/management/usage/feed/cursors/<consumer>` once it is known to be dead.
- An ack is refused when its cursor moves backwards for that consumer or names a
  file that does not exist; re-acking the same cursor is fine.
- Changing `usage-feed` in the config requires a restart.

## Events

Usage v2, one per completed upstream attempt (`status` is `ok` or `error`),
including failed attempts and responses without usage. Install a v2-capable
consumer before restarting an upgraded producer. Historical v1 events remain
readable; v1 all-zero counters cannot distinguish measured zero from absent usage.
A consumer encountering an unsupported version must stop without committing or
acknowledging that page.

```json
{"v":2,"id":"<gateway request id>","at":"2026-10-07T12:00:00.000Z","kind":"usage",
 "key_id":"<first 16 hex of sha256(api key)>","lane":"lane-a","project":"project:822b","task":"task:a929eb4f",
 "account_id":"<gateway auth id>","account_hash":"<sha256 of lowercased account email, or null>",
 "provider":"claude","model":"claude-fable-5-1","effort":"high",
 "tokens":{"input":0,"cached_input":0,"cache_write":0,"output":0,"reasoning":0,"total":0,"unclassified":0},
 "token_status":"complete","token_fields":["input","output"],
 "latency_ms":0,"cache_hit":null,"status":"ok"}
```

Quota, one per quota window observed on the response headers (Claude `5h`, `7d`,
`7d-<family>`; Codex `primary`, `secondary` and named limits):

```json
{"v":1,"id":"<request id>:quota:5h","at":"…","kind":"quota","account_id":"…","account_hash":"…",
 "provider":"claude","scope":"5h","utilization":0.42,"resets_at":"2026-10-07T15:00:00.000Z","exhausted":false}
```

Rules:

- `cached_input` is already inside `input`; `reasoning` is already inside `output`. Never add them again.
- Tokens are normalized from the gateway's token-accounting breakdown, not copied from
  provider counters: `input = uncached + cache_read + cache_write`, `output = non_reasoning +
  reasoning`, `total = input + output + unclassified`, for every provider (Anthropic's `input_tokens`
  excludes cache reads, OpenAI's `prompt_tokens` includes them).
- `token_status` is `complete`, `partial`, `unavailable`, or `invalid`. For unavailable
  or invalid measurements, `tokens` is null. The request outcome is still retained.
  Reported zero input/output is a complete measurement, including on a failed request.
- Partial measurements retain proven non-overlapping buckets and an unclassified
  remainder when a reported total establishes it. They are lower bounds when totals
  are missing. `token_fields` lists normalized `input`, `output`, and `total` totals
  backed by native fields (or the existing complete direct-SDK contract). A partial
  bucket's zero is not proof that the missing native field was reported as zero.
  Never add the partial total to its component buckets again.
- Malformed native numbers and inconsistent arithmetic produce `invalid`, rather
  than coercing strings/null/fractions into a measurement. Quota windows remain
  independent of measurement availability.
- `lane`, `project`, `task` come from the lane key that authenticated the request;
  plain `access.api-keys` give empty strings.
- `account_id` is the selected Gateway auth ID; `account_hash` is an optional email
  hash. No email is in the feed. Capacity-owned Codex IDs are
  `capacity-codex-` plus the first 24 lowercase hex characters of SHA-256(accountRef).
  This distinguishes Personal/Business subscriptions sharing an email. Consumers
  bind only known active IDs and reject conflicting provider/email evidence; an
  unknown owned ID must not fall back to an email or manual-source guess.
- `cache_hit` is `null` until the response reported input tokens.
- `utilization` is a fraction (Codex `used-percent` is divided by 100).

## Endpoints

All under `/v8/management`, behind the management auth middleware, and the actual
socket peer must be loopback (forwarded headers do not count), exactly like
`/capacity/snapshot`. When `usage-feed.enabled` is false they answer
`503 {"code":"USAGE_FEED_DISABLED"}`.

| Path | Method | Body / query | Response |
| --- | --- | --- | --- |
| `/usage/feed` | GET | `cursor=` (omit to start at the oldest retained file), `limit=` (default 500, max 5000), optional `through=` | `{"events":[…],"next_cursor":"<file>:<offset>","has_more":bool,"coverage":{…},"replay":{…}}` |
| `/usage/feed/ack` | POST | `{"consumer":"capacity","cursor":"…"}` | `{"status":"ok", …}`; persisted to `cursors.json` |
| `/usage/feed/cursors` | GET | | `{"cursors":{"capacity":{"cursor":"…","acked_at":"…"}}}` |
| `/usage/feed/cursors/:consumer` | DELETE | | `{"status":"ok"}` or 404; the consumer no longer holds retention |
| `/lane-keys` | POST | `{"lane","project","task","ttl_seconds"}` (lane required; default 8 h, max 30 d) | `201 {"id","key","expires_at",…}` — the key is returned once |
| `/lane-keys` | GET | | `{"lane_keys":[{"id","key_id","lane","project","task","expires_at"}]}` — never the key |
| `/lane-keys/:id` | DELETE | | `{"status":"ok"}` or 404 |
| `/routing/pick` | POST | `{"model","lane"}` | `{"auth_id","account_hash","provider","model","lane","reason","windows":[{"scope","resets_at","exhausted"}]}` |
| `/routing/lanes` | GET | `since=` (RFC 3339; default 48 h back) | `{"read_at","since","today","strategy","truncated","lanes":[{"lane","project","task","state","account","on_since","last_request_at","last_status","requests_today","tokens_today","served_today":[{"account","from","to","requests"}],"pin","keys":[{"id","key_id","expires_at"}]}]}` |
| `/routing/swaps` | GET | `since=` (RFC 3339; default 7 days back) | `{"read_at","since","truncated","swaps":[{"at","lane","project","task","from","to","reason","scope","resets_at"}]}` |
| `/routing/lanes/:lane/pin` | PUT | `{"account":"<gateway auth id>"}` | `{"status":"ok","lane","account","pinned_at"}`; 404 `ROUTING_UNKNOWN_ACCOUNT` |
| `/routing/lanes/:lane/pin` | DELETE | | `{"status":"ok","lane"}`; 404 `ROUTING_NOT_PINNED` |

The cursor is opaque: `"<file>:<byte offset>"`. A reader that stops on a partially
written last line gets the same cursor back and continues once the line is complete.
A cursor naming a retired file resumes at the next retained file and sets
`coverage.missing_cursor`. Every page reports
`coverage: {scope: "retained-files", missing_cursor: boolean, torn_files: number}`.
A torn tail in an older hourly file is counted when advancing past it; an active
last-file tail waits for completion. Corrupt complete JSON lines and invalid byte
boundaries fail the page instead of silently skipping evidence.

For upgrade repair, capture the consumer's committed cursor once as `through` and
read from an independent replay cursor (initially empty). Each page echoes
`replay: {through, complete}`. Reads never pass the fixed byte boundary, even if
new live events arrive. Completion sets `next_cursor` to exactly `through` and
`has_more` to false. If retention removed the boundary, replay still terminates
there and reports `missing_cursor`; it never substitutes a newer boundary.

A replay qualifies only currently retained bytes, not all historical requests.
The replay cursor must never be acknowledged in place of the live cursor. Persist
replay progress with its ingested records; continue ordinary live collection and
acknowledge only committed live positions. A consumer must verify the echoed
replay and coverage metadata: older producers may ignore `through`. Treat that as
unsupported repair while keeping live collection available. Retained-history
replay does not recover failed attempts that an old producer never emitted.

`/routing/pick` runs the manager's read-only selection path (`PeekAuth`) for the
model: the configured selector answers without advancing round-robin state,
spending weighted credits or binding a session, and nothing is executed. `reason` is
`pinned` when the lane is pinned to the chosen credential, `earliest-reset` when the
chosen credential has a fresh, unexhausted window with a future reset, otherwise
`stable-order`. A lane's pin is honoured as in request selection.

## Lanes, swaps and pins

`/routing/lanes` and `/routing/swaps` are derived from the feed and the lane keys;
the gateway keeps no routing history of its own. They are gated like the feed
(loopback peer, feed enabled).

- A lane is every lane named by an unexpired lane key, a usage event in the window,
  or a pin. `account` is the account of the lane's last usage event; `on_since` is
  when that run of consecutive requests on the same account began. `requests_today`,
  `tokens_today` and `served_today` count from the gateway host's local midnight.
  `tokens_today` includes only available measurements and partial lower bounds;
  `partial_usage_today`, `unavailable_usage_today`, and `invalid_usage_today` expose
  incomplete coverage. Historical v1 zero events count as unavailable.
- An account is `{"auth_id","account_hash","label","provider"}`. `label` is the
  Capacity label (`capacity_label` attribute of a Capacity-registered credential)
  when known, otherwise null. No email is answered.
- `state` is one word: `served`, `failing` (the last request errored), `idle` (no
  request in the window) or `pinned-out`.
- A swap is two consecutive usage events of one lane on different accounts. Its
  `reason` is, in order: `pinned` (the lane was pinned to the new account between
  the two requests), `exhausted-window` (the latest quota observation of the old
  account had an exhausted window not yet reset; `scope` and `resets_at` name it),
  `unavailable` (the old account's last request errored), `reset-first` (the
  strategy moved it), otherwise null.
- A pin (`routing.lane-pins`: `lane`, `auth-id`, `pinned-at`) is a preference, not
  a lock: selection tries the pinned credential first and, when it is cooling down,
  exhausted, does not serve the model or was already tried for this request, falls
  back to the configured strategy. The lane then reports `pin.out: true` with
  `out_why` and `out_until`, and `state: "pinned-out"`. `/routing/pick` honours pins
  the same way.

Lane keys are persisted in `access.lane-keys` through the same config writer as
`access.api-keys`, so a hot reload picks them up. Expired keys are refused at auth
time and pruned on every config save.

## Example

```sh
GW=http://127.0.0.1:8317/v8/management
KEY='Authorization: Bearer <management key>'

# issue a lane key for one task
curl -s -X POST "$GW/lane-keys" -H "$KEY" -H 'content-type: application/json' \
  -d '{"lane":"lane-a","project":"project:822b","task":"task:a929eb4f","ttl_seconds":3600}'

# pull the feed from the beginning, then acknowledge
curl -s "$GW/usage/feed?limit=500" -H "$KEY"
curl -s -X POST "$GW/usage/feed/ack" -H "$KEY" -H 'content-type: application/json' \
  -d '{"consumer":"capacity","cursor":"2026-10-07T12Z.jsonl:4096"}'
curl -s "$GW/usage/feed/cursors" -H "$KEY"

# which credential would serve this model right now
curl -s -X POST "$GW/routing/pick" -H "$KEY" -H 'content-type: application/json' \
  -d '{"model":"claude-fable-5-1","lane":"lane-a"}'
```

Checks: `go test ./sdk/cliproxy/usage/feed/ ./internal/api/handlers/management/ ./internal/access/... ./internal/config/`.
The feed package test writes 10,000 events across a writer restart and proves zero
duplicates with a resuming cursor.
