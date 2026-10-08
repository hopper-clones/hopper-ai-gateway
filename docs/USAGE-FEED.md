# Usage feed `hopper.gateway-usage.v1`

The gateway is the only writer of the fact "a request happened". It records that
fact in an append-only JSONL feed that AI Capacity (and only Capacity) pulls over
the loopback management API. Nothing else in the estate parses transcripts for
tokens once this feed is live.

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

Usage, one per completed upstream request (`status` is `ok` or `error`):

```json
{"v":1,"id":"<gateway request id>","at":"2026-10-07T12:00:00.000Z","kind":"usage",
 "key_id":"<first 16 hex of sha256(api key)>","lane":"lane-a","project":"project:822b","task":"task:a929eb4f",
 "account_id":"<gateway auth id>","account_hash":"<sha256 of lowercased account email, or null>",
 "provider":"claude","model":"claude-fable-5-1","effort":"high",
 "tokens":{"input":0,"cached_input":0,"cache_write":0,"output":0,"reasoning":0,"total":0},
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
  reasoning`, `total = input + output`, for every provider (Anthropic's `input_tokens`
  excludes cache reads, OpenAI's `prompt_tokens` includes them). An event whose breakdown
  cannot satisfy this is dropped and counted; its quota events are still written.
- `lane`, `project`, `task` come from the lane key that authenticated the request;
  plain `access.api-keys` give empty strings.
- `account_hash` is the only account identity that leaves the gateway. No email is in the feed.
- `cache_hit` is `null` until the response reported input tokens.
- `utilization` is a fraction (Codex `used-percent` is divided by 100).

## Endpoints

All under `/v8/management`, behind the management auth middleware, and the actual
socket peer must be loopback (forwarded headers do not count), exactly like
`/capacity/snapshot`. When `usage-feed.enabled` is false they answer
`503 {"code":"USAGE_FEED_DISABLED"}`.

| Path | Method | Body / query | Response |
| --- | --- | --- | --- |
| `/usage/feed` | GET | `cursor=` (omit to start at the oldest retained file), `limit=` (default 500, max 5000) | `{"events":[…],"next_cursor":"<file>:<offset>","has_more":bool}` |
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
A cursor naming a retired file resumes at the next retained file.

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
