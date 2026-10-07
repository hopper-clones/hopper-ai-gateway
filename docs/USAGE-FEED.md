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
  acked cursor is past them. With no consumer acked at all nothing is deleted.
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
| `/lane-keys` | POST | `{"lane","project","task","ttl_seconds"}` (lane required; default 8 h, max 30 d) | `201 {"id","key","expires_at",…}` — the key is returned once |
| `/lane-keys` | GET | | `{"lane_keys":[{"id","key_id","lane","project","task","expires_at"}]}` — never the key |
| `/lane-keys/:id` | DELETE | | `{"status":"ok"}` or 404 |
| `/routing/pick` | POST | `{"model","lane"}` | `{"auth_id","account_hash","provider","model","lane","reason","windows":[{"scope","resets_at","exhausted"}]}` |

The cursor is opaque: `"<file>:<byte offset>"`. A reader that stops on a partially
written last line gets the same cursor back and continues once the line is complete.
A cursor naming a retired file resumes at the next retained file.

`/routing/pick` runs the manager's selection path for the model (the configured
selector, reset-first by default) without executing anything. `reason` is
`earliest-reset` when the chosen credential has a fresh, unexhausted window with a
future reset, otherwise `stable-order`. `lane` is echoed for the caller's trace;
selection is lane-agnostic.

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
