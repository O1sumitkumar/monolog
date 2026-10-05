# Telegram daily digest of tasks newly moved to Today

## Overview
- The Telegram bot sends one message per day, at a configured local time, listing only the tasks that *rolled into* Today since yesterday. It does not send the whole Today list.
- Solves: today you only learn a scheduled task arrived in Today by opening the TUI or typing `/today`.
- Fits in as a third goroutine in `telegram.Serve`, next to the pull ticker and the update loop. It is stateless: there is no catch-up after a restart and no "last sent" file.

## Context (from discovery)
- `internal/telegram/sync.go`: `Serve` spawns `runPullTicker` and waits on its done channel; `runUpdateLoop` is the main goroutine. `ServeOptions` carries `Cfg telegram.TelegramConfig`, `Now`, `Writer`.
- `internal/telegram/handler.go`: `Handler` has an injectable `now func() time.Time`, `pullBeforeCommand()`, `listLocked`, `sendBrowseResults` (header + one message per task), and the runtime-only `telegram.TelegramConfig{AllowedUserIDs, PullInterval, BrowseLimit}`.
- `internal/telegram/convert.go`: `FormatTaskRow`, `BuildSummaryKeyboard` (Done / Active / Details).
- `internal/config/config.go`: `config.TelegramConfig`, `telegramBlock` (on-disk JSON, pointer fields), `applyTelegramBlock`, `SaveTelegram` (read-modify-write).
- `cmd/telegram.go`: `status` prints config; `toTelegramPackageConfig` translates `config.TelegramConfig` to `telegram.TelegramConfig` (the two types are separate on purpose, because `internal/telegram` MUST NOT import `internal/config`).
- Tasks store `Schedule` as an ISO date. Bucket names (`tomorrow`, `week`, ...) are resolved to ISO dates at write time, so "rolled into Today" means the date arrived.
- The `internal/telegram` test package's `TestMain` stubs `pullFunc` to return an error, so a digest test sees a failed pre-pull unless it overrides the stub.

## Development Approach
- **testing approach**: Regular (code first, then tests)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**
- run `go test ./...` and `go vet ./...` after each task
- maintain backward compatibility: with no digest keys in `config.json` behavior is unchanged

## Testing Strategy
- **unit tests**: required for every task. Pure helpers (`nextFire`, `newlyToday`, config parsing) get table-driven tests; `sendDigest` and `runDigestTicker` use the existing fake `Bot` and an injected clock.
- no UI e2e tests in this project.

## Progress Tracking
- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix

## Solution Overview
- **Selection rule**: a task is "newly in Today" when `Status == open` AND `Schedule == <today ISO>` AND the UTC date of `CreatedAt` is `< today`. "Today" is the codebase's UTC-date Today (`schedule.todayDate` uses `now.UTC()`, and every `CreatedAt` is written as UTC), so the digest always agrees with `/today`. The configured tz is used **only** to decide when to fire.
  - Exact schedule match means overdue carry-overs don't repeat every day.
  - Recurring spawns are included, since they are created before their scheduled date.
  - Tasks created straight into Today are excluded.
  - Known limitation: an older task manually rescheduled to today is indistinguishable from one that rolled in, so it also appears. This is accepted.
- **Timing**: `nextFire(now, hour, minute, loc)` returns the next occurrence strictly after `now`. A restart after the fire time waits for tomorrow.
- **Delivery**: `chatID == userID` for private chats, so the existing allow-list is the recipient list and no new config is needed.
- **Failure policy**: everything is non-fatal and logged to the `Serve` writer. A bad config value disables the digest and never blocks bot startup.

## Technical Details
- Config on disk (optional keys in the `telegram` block): `"daily_digest_time": "08:00"`, `"daily_digest_tz": "Europe/Riga"`.
  - `config.TelegramConfig` gains raw strings `DigestTime` and `DigestTZ`. `internal/config` stays silent and does no validation (it never writes to stderr; `Load` runs for every CLI command).
  - Validation happens once, in `cmd/telegram.go` (`toTelegramPackageConfig`/`serve`): parse `HH:MM`, resolve the tz (empty means `time.Local`), warn to the serve writer and disable the digest on error. `status` prints `daily_digest: invalid (<reason>)`.
  - `telegram.TelegramConfig` gains already-parsed `DigestEnabled bool`, `DigestHour`, `DigestMinute int` and `DigestLocation *time.Location`. A nil location is treated as `time.Local`.
  - `cmd/telegram.go` imports `time/tzdata` so named zones resolve on minimal hosts.
- `internal/telegram/digest.go`:
  - `nextFire(now time.Time, hour, minute int, loc *time.Location) time.Time` (no error: input is pre-validated)
  - `newlyToday(tasks []model.Task, now time.Time) []model.Task` (unexported, sorted by `Position`; "today" via `schedule.Parse(schedule.Today, now, "")`)
  - `(*Handler) sendDigest(ctx context.Context)`
  - `runDigestTicker(ctx, h, cfg, writer, done)`
  - test seam `var digestAfter = func(d time.Duration) <-chan time.Time { return time.After(d) }`
- Message shape: header `<b>New for Today</b> — N`, then one message per task via `FormatTaskRow` + `BuildSummaryKeyboard`. Nothing is sent when the list is empty.
- `sendDigest` ignores the `readOnly` flag (it is read-only). It takes `h.mu` only via `pullBeforeCommand` and `listLocked`.

## What Goes Where
- **Implementation Steps**: config parsing, `nextFire`, `newlyToday`, `sendDigest`, ticker wiring, status output, tests and docs.
- **Post-Completion**: setting the keys on the bot host's `config.json`, redeploying, and a real-bot smoke test.

## Implementation Steps

### Task 1: Config keys for the digest

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go` (or the existing telegram config test file)

- [x] add raw strings `DigestTime` and `DigestTZ` to `config.TelegramConfig`
- [x] add `DailyDigestTime *string` and `DailyDigestTZ *string` to `telegramBlock` and copy them in `applyTelegramBlock` (no validation, no stderr output)
- [x] extend `SaveTelegram` unconditionally: it builds a fresh four-key map today and would drop the new keys, so write `daily_digest_time` / `daily_digest_tz` from the raw strings (omit when empty)
- [x] write tests: keys parsed, keys absent, `SaveTelegram` round-trip preserves both keys and foreign keys
- [x] run tests - must pass before task 2

### Task 2: Validate in cmd, plumb into the telegram package, show in `status`

**Files:**
- Modify: `internal/telegram/handler.go`
- Modify: `cmd/telegram.go`
- Modify: `cmd/telegram_test.go`

- [x] add `DigestEnabled bool`, `DigestHour`, `DigestMinute int`, `DigestLocation *time.Location` to `telegram.TelegramConfig`
- [x] in `cmd/telegram.go` parse `HH:MM` and resolve the tz; on error warn to the serve writer and leave the digest disabled; import `time/tzdata`
- [x] print `daily_digest: HH:MM <tz>`, `daily_digest: off` or `daily_digest: invalid (<reason>)` in `monolog telegram status`
- [x] write tests: valid, empty (off), bad time, unknown tz, empty tz uses local, both `status` outputs
- [x] run tests - must pass before task 3

### Task 3: `nextFire` helper

**Files:**
- Create: `internal/telegram/digest.go`
- Create: `internal/telegram/digest_test.go`

- [x] implement `nextFire(now, hour, minute, loc)` using `time.Date` in `loc` (DST-safe), returning the next occurrence strictly after `now`; a nil `loc` means `time.Local`
- [x] write tests: before the time, after the time, exactly at the time (expects tomorrow), non-UTC zone, a DST-change day, nil location
- [x] run tests - must pass before task 4

### Task 4: `newlyToday` selection

**Files:**
- Modify: `internal/telegram/digest.go`
- Modify: `internal/telegram/digest_test.go`

- [x] implement `newlyToday(tasks, now)`: open, `Schedule` equals today's ISO date (via `schedule.Parse(schedule.Today, now, "")`), UTC date of `CreatedAt` before today, sorted by `Position`
- [x] a task whose `CreatedAt` fails to parse is skipped
- [x] write tests for the included case (created yesterday, scheduled today)
- [x] write tests for the excluded cases: created today, overdue, future, done, unparsable `CreatedAt`
- [x] write tests for `Position` ordering
- [x] write a parity test near UTC midnight: the digest set equals the exact-date subset of what `schedule.MatchesBucket(..., schedule.Today, now)` returns, so it can never disagree with `/today`
- [x] run tests - must pass before task 5

### Task 5: `sendDigest`

**Files:**
- Modify: `internal/telegram/digest.go`
- Modify: `internal/telegram/digest_test.go`

- [x] implement `(*Handler) sendDigest(ctx)`: `pullBeforeCommand()`, `listLocked(open)`, `newlyToday`; if empty, log and return
- [x] otherwise send the header plus per-task rows with `BuildSummaryKeyboard` to each `AllowedUserIDs` entry; log a failed send and continue with the next user
- [x] write tests with the fake `Bot`: an empty set sends nothing; header + rows reach every allowed user; one user's send error doesn't block the others; the digest still runs while `readOnly` is set; a failed pre-pull is non-fatal
- [x] run tests - must pass before task 6

### Task 6: `runDigestTicker` and `Serve` wiring

**Files:**
- Modify: `internal/telegram/digest.go`
- Modify: `internal/telegram/sync.go`
- Modify: `internal/telegram/digest_test.go`
- Modify: `internal/telegram/sync_test.go`

- [x] implement `runDigestTicker`: compute `nextFire`, wait on `digestAfter(d)` or `ctx.Done()`, call `sendDigest`, then compute the next fire from `max(h.now(), firedTarget)` so a wall clock that lags the monotonic timer (NTP slew) cannot fire the same target twice; close `done` on exit
- [x] in `Serve`, start it only when `Cfg.DigestEnabled` and wait on its done channel in the shutdown path, as for the pull ticker
- [x] write tests with a fake `digestAfter`: fires once and reschedules (asserting the next requested duration), stops promptly on cancel, not started when disabled, no double fire when `now` is 1ms before the target right after firing, `Serve` shuts down cleanly with the digest enabled
- [x] run tests - must pass before task 7

### Task 7: Verify acceptance criteria
- [x] the digest is sent at the configured time, only for newly rolled-in tasks (same Today definition as `/today`), to every allowed user
- [x] disabled by default; invalid config never blocks the bot
- [x] run full test suite: `go test ./...` and `go vet ./...`
- [x] `go build -o monolog` succeeds

### Task 8: [Final] Update documentation
- [x] update the Telegram section of `CLAUDE.md` (config keys, selection rule, no catch-up, goroutine, the `TelegramConfig` field plumbing)
- [x] update the `telegram` block and the `monolog telegram status` row in `README.md`, and the `telegram` block in `docs/deploy/README.md`, with `daily_digest_time` and `daily_digest_tz` (not `env.example`, which holds only env vars); note that an empty tz means the host's local zone, which is often UTC on a systemd host
- [x] move plan (deferred to orchestrator after reviews)

## Post-Completion
*Informational only*

**Manual verification**:
- set `daily_digest_time` a couple of minutes ahead on a test backlog and confirm the message and buttons arrive
- confirm a task scheduled for today but created earlier shows up, and a task added today does not

**External system updates**:
- add the two keys to `config.json` in the bot host's clone (the file is synced via git, or edit it on the host)
- redeploy with `make deploy-bot DEPLOY_HOST=<user@host>`

**Out of scope**: catch-up after downtime, multiple digest times, a `/digest` command, persisted last-sent state.
