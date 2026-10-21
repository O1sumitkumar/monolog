# TUI: Open Task in Claude Code

## Overview
- One key (`o`) on the selected task in the TUI opens a new terminal tab (kitty, via a configurable command) running Claude Code with the prompt `Lets work on monolog item <full ULID> <title>\n\n<body>`, and marks the task active.
- Problem: tasks filed by the Claude Code skill (`claude` tag, body = `<repo/path:line> + why`) are meant to be picked up later in a Claude session; today that means copying ID/title/body by hand and opening a tab in the right repo.
- The working directory comes from the task's tags via a tag → dir map (with a fallback), so a `paperwall` task opens in the paperwall repo and a `monolog` task in the monolog repo.
- **Skill-filed tasks carry no repo tag**: `docs/claude-skill/SKILL.md:100` puts the repo in the body (`monolog/internal/...`), not a tag, so a fresh `claude`-only task resolves to `default_dir`. Two supported workflows: retag during triage (`t`) so the map fires, or set `default_dir` to the parent of all repos — the body's repo-qualified path then resolves relative to Claude's cwd. Body-path parsing is deliberately out of scope (decided in brainstorm).
- Launch first, activate only on success: a misconfigured launcher never leaves a stray activation commit/push behind.
- TUI only — no CLI command (YAGNI).

## Context (from discovery)
- files/components involved:
  - `internal/config/config.go` — `EmailConfig`/`TelegramConfig` pattern: block struct, `reset*CfgToDefaults`, `apply*Block`, `Load` decoding into a local struct; `Save`/`SaveEmail`/`SaveTelegram` are read-modify-write over a `map[string]any`
  - `internal/tui/model.go` — `toggleActive` (`:1538`) does move-to-Today + `ordering.PositionTop` + whole-bucket rebalance + multi-file `AutoCommitSHA`, status computed inside the cmd; `updateNormal` key switch (`:1262+`, `o` is free); `newModel` (`:410`) snapshots `config.Email()` / `config.AutoPush()`; `helpLine` / `renderHelpBar` (`:3425+`, two hint lists: tag view and schedule view); help overlay text (`:3707`); `clampStatus` (`:3417`); seam precedent `var runAutoPush = git.AutoPush` (`:2819`)
  - `internal/store/store.go` — `Store.Get(id)` for re-finding the task by ID
  - `internal/git/git.go` — `ShortError` (`:687`) and the `waitDelayAfterSuccess` precedent for `exec.ErrWaitDelay` after a successful exit
  - `docs/claude-skill/SKILL.md` — body convention `monolog/internal/store/store.go:142` (repo-qualified "where")
- related patterns found:
  - lower-level packages never import `internal/config`; callers snapshot config once and pass values in
  - every TUI mutation goes through `taskSavedMsg{status, focusID, sha}` so undo stack + auto-push dispatch work automatically
  - existing external process: `tea.ExecProcess` for `$EDITOR` (suspends TUI) — deliberately NOT used here
- dependencies identified: none new (stdlib `os/exec`, `context`, `strings`)

## Development Approach
- **testing approach**: Regular (code first, then tests in the same task)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional - they are a required part of the checklist
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change
- maintain backward compatibility (absent `launch` block = feature off, no behavior change)

## Testing Strategy
- **unit tests**: required for every task (see Development Approach above)
- **e2e tests**: project has no UI e2e suite; TUI behavior is covered by `internal/tui/model_test.go`-style model tests driving `Update` with key messages against a temp git-backed store, with `runLaunch` swapped for a fake

## Progress Tracking
- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview
- **Config**: optional `"launch"` block in `<MONOLOG_DIR>/.monolog/config.json`, read by `config.Launch() LaunchConfig`. Not added to the `git.Init` template (opt-in, per-machine paths). Accepted trade-off: machine-specific paths live in the synced repo config, like `email`/`telegram`.
- **Pure package `internal/launch`** (no `internal/config` import): `ResolveDir`, `BuildPrompt`, `BuildArgv`, and `Run` (the only impure function — exec with timeout).
- **TUI**: `o` in normal mode → validate + resolve → `launchCmd` goroutine runs the command → `launchResultMsg` → on success re-find task by ID and, if not already active and not done, activate through the extracted `activateCmd` (same commit/rebalance/undo/auto-push path as `a`).
- **Why no shell**: task bodies contain quotes, newlines, backticks, `$()`; an argv array with per-element substitution passes the prompt as exactly one argument with no escaping and no injection surface.
- **Why not `tea.ExecProcess`**: it hands the terminal to the child and suspends the TUI; here the child (`kitty @ launch`) returns as soon as the tab exists and the TUI should stay put.
- **Why `Setsid`**: leaving stdio unattached is not enough — a child can open `/dev/tty` directly (same failure CLAUDE.md documents for git/ssh). `kitty @` does exactly that when `KITTY_LISTEN_ON` is unset: it writes its request escape to the tty and reads the reply, racing Bubble Tea's raw-mode reader (reply bytes parsed as keystrokes, hang until timeout). Running the child in a new session (`SysProcAttr{Setsid: true}`) gives it no controlling terminal, so `/dev/tty` fails with ENXIO and a missing `listen_on` surfaces as a clean `launch failed: …`. Darwin + linux are the only release targets.
- **Double press**: pressing `o` twice opens two tabs and may queue two `activateCmd`s; the second is a no-op commit via `AutoCommitSHA`'s nothing-staged path. No in-flight guard (YAGNI).

## Technical Details

### Config shape
```json
"launch": {
  "command": ["kitty", "@", "launch", "--type=tab", "--cwd={dir}", "claude", "{prompt}"],
  "dirs": { "paperwall": "~/IdeaProjects/paperwall", "monolog": "~/IdeaProjects/monolog" },
  "default_dir": "~/IdeaProjects"
}
```
- `config.LaunchConfig{Command []string; Dirs map[string]string; DefaultDir string}`; zero value = disabled. "Configured" means `len(Command) > 0`.
- `launchBlock` JSON struct (`command`, `dirs`, `default_dir`), `resetLaunchCfgToDefaults()` at the top of `Load`.
- **Decoded in isolation**: `Load` decodes the whole file into one struct and a single `json.Unmarshal` error resets *every* setting (`return nil // malformed JSON, keep defaults`, `config.go:371`). `launch` has no writer command, so it is the block most likely to be hand-typed wrong (e.g. `"command": "kitty @ launch"` as a string) — and config.json is synced, so that would disable telegram on the bot host at its next restart. So the `Load` struct declares `Launch json.RawMessage \`json:"launch,omitempty"\``, unmarshalled separately into `launchBlock`; on error launch stays zero-valued and nothing else is affected.

### `internal/launch`
- `ResolveDir(tags []string, dirs map[string]string, defaultDir string) (string, error)`: iterate task tags in order, skip reserved `active` and provenance `claude` (the skip only matters if a user maps one of them — doc comment says so); first tag present in `dirs` wins; else `defaultDir`; else `fmt.Errorf("no launch dir for tags %v", tags)`. Result passed through `expandHome` (leading `~` or `~/` only, via `os.UserHomeDir`).
- `BuildPrompt(id, title, body string) string`: `"Lets work on monolog item " + id + " " + title`, plus `"\n\n" + body` only when `strings.TrimSpace(body) != ""` (body right-trimmed).
- `BuildArgv(command []string, dir, prompt string) []string`: new slice, each element passed through one `strings.NewReplacer("{dir}", dir, "{prompt}", prompt)` — single pass, so a literal `{dir}` inside the body is NOT re-substituted.
- `Run(ctx context.Context, argv []string, dir string) error`: `exec.CommandContext(ctx, argv[0], argv[1:]...)`, `cmd.Dir = dir` (so launchers without a `--cwd` flag still work), `cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}` (no controlling tty — see Solution Overview), stdin unset (`/dev/null`), `CombinedOutput`, `cmd.WaitDelay` bounded (~1s). Treat `exec.ErrWaitDelay` with a successful `ProcessState` as success (a launcher whose grandchild inherits the pipe must not be reported as failed — same reasoning as `git.waitDelayAfterSuccess`). On failure return `fmt.Errorf("%w: %s", err, firstLine(output))` (output part omitted when empty). Empty argv → error.

### TUI flow (`o` key)
1. No selected task → no-op.
2. `!m.launchConfigured()` → `statusMsg = "launch not configured"`, return nil.
3. `ResolveDir` error → `statusMsg` with that message; `os.Stat(dir)` failure or not a dir → `"launch dir not found: <path>"`. Nothing runs. **All launch messages go through `statusMsg`, never `m.err`**: `m.err` is only cleared by a successful `taskSavedMsg` (`model.go:1043`) and `helpLine` renders it ahead of `statusMsg` (`:3426`), so a sticky error would hide every later `launched:` status.
   - `o` on a task that is already done (Done tab) launches but never activates — reviewing a finished task in Claude is legitimate.
4. `launchCmd(argv, dir, taskID, title)` → goroutine: `ctx, cancel := context.WithTimeout(context.Background(), launchTimeout)` (5s), `err := runLaunch(ctx, argv, dir)`, return `launchResultMsg{err, taskID, title}`.
5. `Update(launchResultMsg)`:
   - `err != nil` → `statusMsg = "launch failed: " + <err>` (rendered via `clampStatus`), no activation, no commit.
   - else `t, err := m.store.Get(taskID)`: `errors.Is(err, store.ErrNotFound)` → `"launched: <title> (task gone, not activated)"`; any other error → `"launched: <title> (not activated: <err>)"`; done or already active → `"launched: <title>"` and **no toggle** (toggling an active task would deactivate it); otherwise `return m, m.activateCmd(t, "launched")` whose status is `"launched: <title>"` / `"launched: <title> (moved to Today)"`.
- Seam: `var runLaunch = launch.Run` next to `runAutoPush`.
- `Model.launchCfg config.LaunchConfig` snapshotted in `newModel` from `config.Launch()` (not threaded through `Options`, same reasoning as `autoPushEnabled`).

### `toggleActive` refactor
- Extract the activation half (move-to-Today, `PositionTop`, rebalance, multi-file commit, status) into `func (m *Model) activateCmd(t model.Task, verb string) tea.Cmd`, where `verb` prefixes the status (`"activated"` from `toggleActive`, `"launched"` from the launch handler). `activateCmd` sets the `active` tag and `UpdatedAt` itself. `toggleActive` keeps the deactivate branch and calls `activateCmd(*task, "activated")` for the activate branch. Behavior of `a` unchanged.

## What Goes Where
- **Implementation Steps** (`[ ]` checkboxes): config block, `internal/launch` package, TUI refactor + key + result handling, help text, README/CLAUDE.md docs
- **Post-Completion** (no checkboxes): kitty.conf changes on the user's machine, writing the actual `launch` block with real paths, manual end-to-end run in kitty

## Implementation Steps

### Task 1: Add `launch` config block

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [x] add `LaunchConfig` struct (doc comment noting `internal/launch` must not import config), package var, `resetLaunchCfgToDefaults`, `launchBlock` JSON struct
- [x] wire into `Load`: reset at top, `Launch json.RawMessage \`json:"launch,omitempty"\`` field, separate `json.Unmarshal` into `launchBlock`; on error leave launch zero-valued without returning early
- [x] add `Launch() LaunchConfig` accessor returning a copy (copy the `Command` slice and `Dirs` map so callers cannot mutate package state)
- [x] write tests: full block parses (command order preserved, dirs, default_dir); absent block → zero value; `Load` on a second dir without the block resets a previously loaded block; returned value is a defensive copy
- [x] write test: launch block with wrong types (`"command": "kitty @ launch"`) leaves launch disabled **and** `date_format` and `telegram.enabled` from the same file still load
- [x] write one round-trip test: `Save` preserves an existing `launch` block (re-`Load` and compare) — the other writers share the same map read-modify-write already covered by existing foreign-key tests
- [x] run `go test ./internal/config/` - must pass before task 2

### Task 2: Pure launch helpers (`ResolveDir`, `BuildPrompt`, `BuildArgv`)

**Files:**
- Create: `internal/launch/launch.go`
- Create: `internal/launch/launch_test.go`

- [x] create package doc + `ResolveDir` with tag-order resolution, skipping `active` and `claude`, `default_dir` fallback, and the `no launch dir for tags [...]` error
- [x] add `expandHome` (leading `~` / `~/` only; `~user` and mid-path `~` untouched)
- [x] add `BuildPrompt(id, title, body)` and `BuildArgv(command, dir, prompt)` using a single-pass `strings.NewReplacer`
- [x] write table tests for `ResolveDir`: first matching tag wins by task-tag order; `active`/`claude` skipped even if mapped; unmapped tags → default; no match + empty default → error naming the tags; `~` and `~/x` expanded (via `t.Setenv("HOME", ...)`); `~user/x` untouched
- [x] write table tests for `BuildPrompt` (empty/whitespace body → no trailing blank lines; multi-line body preserved) and `BuildArgv` (placeholders replaced in every element including mid-string `--cwd={dir}`; prompt with quotes, newlines, backticks and `$(rm -rf ~)` survives as one exact element; literal `{dir}` inside the prompt not re-substituted; input slice not mutated; elements without placeholders unchanged)
- [x] run `go test ./internal/launch/` - must pass before task 3

### Task 3: `launch.Run` process execution

**Files:**
- Modify: `internal/launch/launch.go`
- Modify: `internal/launch/launch_test.go`

- [x] implement `Run(ctx, argv, dir)` with `exec.CommandContext`, `cmd.Dir`, `Setsid: true`, bounded `WaitDelay`, `CombinedOutput`, first-line-of-output error wrapping, empty-argv error
- [x] write test: child cannot reach a controlling terminal — `sh -c ': </dev/tty'` returns an error
- [x] treat `exec.ErrWaitDelay` with a successful `ProcessState` as success
- [x] write tests (real `sh`, skip on Windows): exit 0 → nil; `sh -c 'echo boom >&2; exit 3'` → error containing `boom`; missing binary → error; `cmd.Dir` honored (`sh -c 'pwd > out'` in a temp dir); argv element containing spaces/newlines reaches the child verbatim (`printf %s "$1" > file`)
- [x] write tests: context timeout kills a `sleep 10` within the deadline; `sh -c 'sleep 5 & exit 0'` (background child holding the pipe) returns nil within ~WaitDelay
- [x] run `go test ./internal/launch/` - must pass before task 4

### Task 4: Extract `activateCmd` from `toggleActive`

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

- [ ] extract the activation branch of `toggleActive` into `activateCmd(t model.Task, verb string) tea.Cmd` (sets `active` tag + `UpdatedAt`, move-to-Today, `PositionTop`, rebalance, multi-file `AutoCommitSHA`, status `"<verb>: <title>"` + `" (moved to Today)"` when moved)
- [ ] make `toggleActive` call `activateCmd(*task, "activated")`; deactivate branch unchanged
- [ ] confirm existing `a`-key tests pass unchanged (status strings, move-to-Today, overdue not clobbered, rebalance commits siblings)
- [ ] write tests for `activateCmd` with verb `"launched"`: status prefix, moved-to-Today suffix only when bucket changed, returned `taskSavedMsg.sha` non-empty
- [ ] run `go test ./internal/tui/` - must pass before task 5

### Task 5: `o` key, launch command and result handling

**Files:**
- Modify: `internal/tui/model.go`
- Create: `internal/tui/launch_test.go`

- [ ] add `launchCfg config.LaunchConfig` to `Model`, snapshot in `newModel`, `launchConfigured()` helper, `launchTimeout = 5 * time.Second`, `var runLaunch = launch.Run`
- [ ] add `launchSelected()` handling steps 1–4 (no task, not configured, resolve dir, `os.Stat`) and `launchCmd` returning `launchResultMsg{err, taskID, title}`; bind `"o"` in `updateNormal`
- [ ] handle `launchResultMsg` in `Update`: failure status, re-find by ID via `m.store.Get` (`ErrNotFound` vs other errors), skip activation when gone/done/already active, otherwise `activateCmd(t, "launched")`; every message via `statusMsg`, never `m.err`
- [ ] test setup: in the tui `TestMain`, default `runLaunch` to a stub returning an error (like `runAutoPush` is neutralized) so no test can ever exec kitty; most tests set `m.launchCfg` directly; one config-driven test writes the `launch` JSON before `config.Load` (mirror `newTestModelWithEmail`, `email_test.go:102`) to pin the `newModel` snapshot
- [ ] write tests (fake `runLaunch` recording argv/dir; `t.Cleanup` restores it): inactive task in Week → launch called with resolved dir and prompt as one argv element containing full ULID/title/body → task active, moved to Today, one new commit, undo stack grew, status `launched: … (moved to Today)`
- [ ] write tests: already-active task stays active and no new commit; failed launch → status `launch failed: …`, no commit, task unchanged; task deleted before result → no panic, status says not activated; `o` on a done task (Done tab) → launch called, not activated
- [ ] write test for the selection race without `runCmd` (it follows the whole chain): `cmd := key(t, m, "o")`, `msg := cmd()`, move cursor with `j`, `m.Update(msg)`, then run the rest of the chain → the launched task (by ID) is activated, not the newly selected one
- [ ] write test: `o` failing dir resolution, then a successful `o` → help line shows `launched: …` (no sticky error)
- [ ] write tests: unconfigured → status `launch not configured` and `runLaunch` never called; no matching tag and no default → error status, not called; mapped dir missing on disk → `launch dir not found`, not called; detail panel open → `o` goes to the note textarea, not called
- [ ] run `go test ./internal/tui/` - must pass before task 6

### Task 6: Help bar hint and help overlay

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/launch_test.go`

- [ ] add `{"o", "claude"}` right after `{"a", "active"}` in both `renderHelpBar` hint lists (tag view + schedule view) only when `launchConfigured()` — early placement because `renderHelpBar` drops hints from the right when they exceed `m.width` (`model.go:3393`); the variadic literals become slices for the conditional insert
- [ ] add an `o    open in Claude Code (and activate)` line to the `h` help overlay (always listed, mirrors other keys)
- [ ] write tests (with `m.width` wide enough): help line contains `claude` hint when configured, absent when not (both view modes); help overlay contains the `o` line
- [ ] run `go test ./internal/tui/` - must pass before task 7

### Task 7: Verify acceptance criteria
- [ ] verify all requirements from Overview are implemented (key, tag → dir, prompt shape, launch-then-activate, TUI-only, off when unconfigured)
- [ ] verify edge cases are handled (already active, done, deleted mid-flight, selection moved, missing dir, no shell interpretation of body)
- [ ] run full test suite: `go test ./...`
- [ ] run `go vet ./...` and `go build -o monolog`
- [ ] verify every new exported function in `internal/launch` and the new config accessor has direct tests

### Task 8: [Final] Update documentation
- [ ] README.md: add `o` row to the TUI keybinding table; add an "Open a task in Claude Code" section with the example `launch` block, the tag → dir resolution rule (tag order, `active`/`claude` skipped, `default_dir` fallback), the skill-filed-task workflow (no repo tag per SKILL.md → retag with `t`, or set `default_dir` to the parent of your repos), the fixed prompt, launch-then-activate behavior, and kitty setup (`allow_remote_control socket-only` + `listen_on unix:/tmp/kitty` in `kitty.conf` so `KITTY_LISTEN_ON` is exported and `kitty @` uses the socket rather than the tty, which would clash with Bubble Tea's alt-screen); note non-kitty examples are a config change only
- [ ] CLAUDE.md: add `internal/launch/` to the Architecture list and a **Launch in Claude Code (TUI)** Key Design Decisions bullet (config block + `config.Launch()`, no-config-import rule, argv-not-shell, single-pass replacer, launch-then-activate-by-ID, already-active must not toggle, `runLaunch` seam, `activateCmd` shared with `a`)
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion
*Items requiring manual intervention or external systems - no checkboxes, informational only*

**Manual verification**:
- add `allow_remote_control socket-only` and `listen_on unix:/tmp/kitty` to `~/.config/kitty/kitty.conf`, restart kitty, confirm `echo $KITTY_LISTEN_ON` is set
- add the real `launch` block to `~/.monolog/.monolog/config.json` (or wherever `MONOLOG_DIR` points) with real repo paths
- in kitty: run `monolog`, select a task tagged `paperwall` (retag with `t` if needed), press `o` → new tab in the paperwall repo with Claude Code started on the prompt; task turns green and moves to Today; `u` undoes the activation (tab stays open)
- press `o` on a `claude`-only task → tab opens in `default_dir`
- remove `listen_on` from kitty.conf, restart kitty, press `o` → status `launch failed: …`, alt-screen intact, no stray keystrokes
- press `o` on an already-active task → new tab, task stays active
- break the config (e.g. `kitty` → `kittyx`) → status `launch failed: …`, no commit
- confirm a body with quotes/newlines arrives intact in the Claude prompt

**External system updates**:
- if the second device also runs the TUI, its repo layout must match the `dirs` paths (config.json is synced), or it should rely on `default_dir`
