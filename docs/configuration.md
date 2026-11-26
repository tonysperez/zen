# Configuration

Zen reads `~/.zen/config.yaml`. Run `zen setup` for an interactive walkthrough, or edit the file directly.

## Minimal example

```yaml
repos:
  app:
    full_name: octo-sts/app
    base_path: ~/git/repo-octo-sts-app

authors:
  - mattmoor
  - wlynch
```

That's enough to start the daemon (`zen watch start`) and check your inbox (`zen inbox`). `authors` is who the daemon auto-creates worktrees for. You still get notified for other review requests; `zen review` can open those by hand.

## Full example

```yaml
repos:
  app:
    full_name: octo-sts/app
    base_path: ~/git/repo-octo-sts-app

authors:
  - mattmoor
  - wlynch

poll_interval: "5m"

# Which coding agent zen launches in worktrees: "claude" (default) or "codex".
agent: claude
claude_bin: claude   # executable used when agent: claude
codex_bin: codex     # executable used when agent: codex

terminal: iterm  # or "ghostty", "kitty", "macos", or "tmux"; unset means tmux inside tmux, else iterm

# Prefix for feature branches created by `zen work new`.
# If unset, falls back to `git config user.name` (spaces → hyphens), then no prefix.
branch_prefix: mgreau

# Skip draft PRs in `zen inbox`, watch notifications, and the MCP inbox tool.
# Defaults to false (drafts are shown). Set to true to skip drafts so you don't
# review something that isn't ready. Override on a single run with
# `--ignore-drafts=false`. Watch does not refresh a hidden draft (or a closed
# PR); when the PR is ready again, SHA comparison catches up. A live check at
# reconcile time covers the race where a PR is queued while ready then drafted.
ignore_drafts: true

watch:
  dispatch_interval: "10s"      # How often to process queued work
  cleanup_interval: "1h"        # How often to scan for merged PRs
  cleanup_after_merge: "1h"     # Time after a PR merges before its review worktree is removed
  session_scan_interval: "10s"  # How often to scan Claude session states
  concurrency: 2                # Parallel worktree setups
  max_retries: 5                # Max retry attempts for git failures

# Slack task watcher (opt-in, off by default) — see "Slack task watcher" below.
slack:
  enabled: true
  emoji: claudecode      # reaction name (no colons), default "claudecode"
  default_repo: mono     # short repo name worktrees are created in (route: agent only)
  route: agent           # "agent" (default): worktree + agent; "event": emit slack_task only
  poll_interval: "5m"
  ack_reaction: eyes      # reaction added on pickup, default "eyes"
  completion_dm: true     # DM you when a Slack-started session goes idle, default true

# Notification routing — see "Notification routing" below.
notify:
  desktop: true          # daemon desktop notifications, default true
  command: ""            # run via `sh -c` for each event in `events`, default none
  events: [review_requested, session_waiting, slack_task]   # the default

# Event log (opt-in, off by default) — see "Event log" below.
events:
  enabled: true          # append daemon events to ~/.zen/state/events.jsonl
```

The daemon re-reads `config.yaml` on every poll tick. Changes to `poll_interval`, `authors`, `repos`, and other settings take effect without restarting.

On each poll, setup re-runs if GitHub's head SHA differs from the worktree. Linear updates fast-forward. Local edits and a live agent are left alone. A head that cannot be fast-forwarded onto — rewritten, or behind the worktree — waits for `zen review`, which asks before `reset --hard` and only from a terminal.

## Merged-review cleanup

Every `cleanup_interval`, the daemon removes the review worktree of each PR that merged at least `cleanup_after_merge` ago. The delay is a Go duration such as `30m` or `6h`, measured from GitHub's merge time; it defaults to `1h`, and `0s` removes the worktree on the next scan.

Only PR reviews are removed: worktrees named `<repo>-pr-<N>` that are checked out on the `pr-<N>` branch zen created, or on a detached HEAD. Work streams are never removed by the daemon, even when their name ends in `-pr-<N>`; `zen cleanup` is the manual command for those. PRs closed without merging are left alone too.

A merged review is kept, and the skip is logged, when any of these is true:

- It has local changes, or untracked files other than zen's generated context.
- An agent is running in it.
- `HEAD` has commits that the PR's last head on GitHub does not contain. Zen never pushes from a review worktree, so those commits exist only there.
- The comparison can't be made, for example because `pull/<N>/head` can't be fetched.

`cleanup_after_days` is deprecated and ignored. It delayed removal until a merged review had no new commits for that many days, which let merged reviews pile up.

## Repos

Each repo key (e.g. `app`) is a short name you choose — it doesn't have to match the GitHub repo name. It's used for worktree naming (`app-pr-42`), queue keys (`app:42`), and display. The `full_name` is the actual `owner/repo` used for GitHub API calls. The daemon polls review requests across every repo in this file.

If two orgs have a repo with the same name, pick different keys:

```yaml
repos:
  octo-app:
    full_name: octo-sts/app
    base_path: ~/git/repo-octo-sts-app
  other-app:
    full_name: other-org/app
    base_path: ~/git/other/repo-app
```

All repos and authors must be configured — there are no hardcoded defaults.

## Agent

`agent: claude` (default) or `agent: codex` selects the coding agent zen launches in each worktree. Override per command with `--agent`:

```bash
zen review 42 --agent codex
zen work new app my-feature --agent codex
zen review resume 42 --agent codex
```

What changes per agent:

| | `claude` | `codex` |
|--|----------|---------|
| Launch / resume | `claude … --resume <id>` | `codex … / codex resume <uuid>` |
| Model flag | `--model` | `-m` |
| Context file | `CLAUDE.local.md` | `AGENTS.md`, or `.zen/PR_CONTEXT.md` if the repo already ships an `AGENTS.md` |
| Slash-command prompts | `~/.claude/commands/` | `~/.codex/prompts/` |
| Sessions on disk | `~/.claude/projects/<encoded-path>/*.jsonl` | `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` |

zen never overwrites a repo's committed `AGENTS.md`; if one exists, the context goes to `.zen/PR_CONTEXT.md` instead. The zen-owned `.zen/` directory is added to the repo's shared `info/exclude` (git has no per-worktree exclude file), while an injected `AGENTS.md` shows as an untracked file — the same way Claude's injected `CLAUDE.local.md` does. The `--model` value is passed through verbatim, so use model names your chosen agent understands (e.g. `opus` for Claude, `gpt-5-codex` for Codex).

Note that the background daemon always uses the **configured** agent: if you run `zen review 42 --agent codex` while `agent: claude` is set in config, the daemon's context injection and session tracking for that worktree still act as Claude. Set `agent:` in config when switching agents for more than a one-off session.

`zen agent status` (and the `zen_agent_status` MCP tool) is the exception to single-agent behaviour: it lists sessions from **all** agents in one table with an AGENT column; pass `--agent claude|codex` to narrow it. "waiting" detection comes from the daemon's snapshot and is only available for the configured agent — other agents' sessions show as running/stopped.

### Session migration between agents

`zen review resume`/`zen work resume` can carry a session across agents: when the selected agent has no session for the worktree but the other agent does, zen offers to migrate the most recent one and resume it (pass `--migrate` to skip the prompt). Claude → Codex goes through Codex's own importer via `codex app-server` (with its limits: last 30 days, ~50 most recent sessions); Codex → Claude is a zen-side translation of the rollout into a Claude session file (shell calls become `Bash` tool history, other tool calls become annotated text, encrypted reasoning is dropped, and files are capped below Claude's 5 MB resume threshold). Migration formats were verified against Claude Code 2.1.200 and Codex CLI 0.142.0; both agents' session formats are undocumented internals, so a future agent update can require adjustments. Deep dive: [session-migration.md](session-migration.md).

## Terminal

`terminal: iterm`, `terminal: ghostty`, `terminal: kitty`, `terminal: macos`, or
`terminal: tmux`. When `terminal` is unset, zen uses `tmux` if it runs inside a
tmux session (`$TMUX` is set) and `iterm` otherwise.

For Ghostty tab creation on macOS:

1. **Ghostty must be running** — open it manually before invoking zen.
2. **Accessibility permissions** — System Preferences → Security & Privacy → Accessibility.
3. **Automation permissions** — System Preferences → Security & Privacy → Automation.
4. **Ghostty focus** — the window should be focused for reliable tab creation.

If any of these aren't met, zen falls back to opening new windows.

kitty (Linux and macOS) opens each session in a new OS window. When zen runs
from inside kitty with `allow_remote_control yes` set in `kitty.conf` (or a
socket configured via `listen_on`), the window is opened from the running
kitty instance; otherwise zen starts a separate kitty instance per session.

`terminal: macos` uses the built-in macOS Terminal.app. If Terminal already has
a window, zen opens a new tab via Cmd+T (needs Accessibility, same as Ghostty);
if none exist, or tab creation fails, it opens a new window. macOS-only.

`terminal: tmux` opens each session as a new window in the tmux session zen
runs in, named after the worktree directory. It suits a remote host reached
over ssh, such as a cloud workstation, where there is no terminal application
to drive. The command runs through your login shell (`$SHELL -l -c`), and the
window keeps a shell in the worktree when the agent exits. Outside tmux, zen
reports an error; use `--no-terminal` there instead.

## Slack task watcher

An opt-in daemon loop that polls for the user's own emoji reaction (`slack.emoji`, default `claudecode`) via `reactions.list`, then for each new hit: adds an ack reaction in-thread (`slack.ack_reaction`, default `eyes`), fetches the thread, creates a feature worktree in `slack.default_repo` from `origin/main`, and immediately launches the agent in a terminal tab with the thread as the initial prompt — the launched session does all the "figure out what to do" work, not the daemon. Unlike the PR-review flow, this does open a terminal tab automatically: a Slack reaction is a deliberate, comparatively rare signal, not the high-volume PR-review firehose.

When that worktree's session later goes idle ("waiting", the same signal that drives the desktop `notify.SessionWaiting`), the daemon also sends a Slack DM to the user with the worktree name, resume command, and a link back to the original thread. That DM stops once the task is actually done: either a merged PR from the worktree's branch, or the configured `slack.done_emoji` (default `done_check`) reacted onto the original message.

Setup:

1. `slack.enabled: true` in config, plus `slack.default_repo` (required for the default `route: agent` — no hardcoded default).
2. Create a Slack app (a dedicated personal one is recommended over reusing a shared org bot) with these token scopes: `reactions:read`, `channels:history`, `groups:history`, `im:history`, `mpim:history`, `chat:write`.
3. Export the resulting token as `ZEN_SLACK_TOKEN` in the environment the daemon starts in (`zen watch start` inherits your shell's env). The token is never written to `config.yaml` or any state file.

If `slack.enabled` is true but `ZEN_SLACK_TOKEN` is unset, or `auth.test` fails at startup, the daemon logs a warning and runs with the Slack watcher disabled rather than failing to start.

Set `slack.completion_dm: false` to stop the idle DM (see [Notification routing](#notification-routing)).

### Handing tasks to something else: `slack.route: event`

`slack.route: agent` (the default) is the behaviour above. With `slack.route: event`, pickup is the same up to the point of acting: the daemon adds the ack reaction, marks the message seen, and fetches the thread. Then, instead of creating a worktree and launching an agent, it emits a `slack_task` event: to the [event log](#event-log) when `events.enabled` is on, and to `notify.command` when `slack_task` is in `notify.events`. The event carries the channel, message timestamp, permalink, the thread author (`data.user`), and the thread text (up to 20,000 bytes, with `data.truncated` when cut). A supervisor, such as a lead agent session, decides what to do with it. No worktree is recorded for the thread, so no completion DM follows.

`slack.default_repo` is only required for `route: agent`. With `route: event` and `slack.enabled: true`, config loading fails unless the event has somewhere to go: `events.enabled: true`, or a `notify.command` whose `notify.events` include `slack_task`. Any other `route` value fails config loading.

```yaml
slack:
  enabled: true
  route: event
notify:
  command: "~/bin/tell-lead"   # gets the slack_task JSON on stdin
```

## Event log

With `events.enabled: true`, the daemon appends one JSON object per line to `~/.zen/state/events.jsonl` (or `$ZEN_HOME/state/events.jsonl`) whenever something happens that a supervisor, such as another agent session, might act on. A watcher can tail that one file instead of polling `zen inbox` and the worktrees. The log is off by default, and turning it on changes nothing else the daemon does.

Each line has `time` (RFC3339, UTC) and `type`, plus whichever of these apply: `repo` (short name from `repos:`), `pr`, `worktree` (path), `session` (agent session ID), `sha`, `url`, and a small `data` object.

| Type | When | Notable fields |
|------|------|----------------|
| `review_requested` | First sighting of a review request with no local worktree (the same moment as the "New PR Review Request" notification) | `repo`, `pr`, `sha`, `url`; `data.title`, `data.author`, `data.draft`, `data.auto_create` (author is in `authors:`) |
| `review_prepared` | The daemon created a review worktree | `repo`, `pr`, `worktree`, `sha`, `url`; `data.title`, `data.author` |
| `pr_updated` | A review worktree was fast-forwarded to a new GitHub head | `repo`, `pr`, `worktree`, `sha`; `data.old_sha`, `data.new_sha` |
| `session_waiting` | An agent session went from running to waiting for input (debounced to once per 5 minutes per session, like the desktop notification) | `worktree`, `session`, `pr` for reviews; `data.name`, `data.kind`, `data.model`, `data.resume` |
| `session_stopped` | A running or waiting session's process exited | as `session_waiting` |
| `review_cleaned` | Merged-review cleanup removed a review worktree | `repo`, `pr`, `worktree`, `sha` (PR head), `url`; `data.merged_at` |
| `slack_task` | The Slack watcher picked up a flagged message | `url` (permalink); `data.channel`, `data.ts`, `data.permalink`, `data.user` (thread author), `data.flagged_by`, `data.text` (thread, up to 20,000 bytes), `data.route`; with `route: agent` also `repo`, `worktree`, `data.branch` |

Example:

```json
{"time":"2026-01-02T15:04:05Z","type":"review_requested","repo":"app","pr":42,"sha":"3f2c…","url":"https://github.com/octo-sts/app/pull/42","data":{"author":"wlynch","auto_create":true,"draft":false,"title":"Fix token refresh"}}
```

Each line is written with a single `O_APPEND` write, so a reader never sees half a line. At 10MB the log is renamed to `events.jsonl.1` (replacing the previous backup) and a new one is started, as `watch.log` is.

Read it with `zen events`:

```bash
zen events                                   # everything, oldest first (backup, then current)
zen events --since 2h                        # last two hours; also takes an RFC3339 time
zen events --type review_requested,slack_task
zen events --since 30m --follow              # keep printing new events; survives rotation
zen events --json                            # one JSON array instead of lines
```

Output is the raw JSON lines, so it pipes into `jq`. `--follow` always streams lines.

## Notification routing

By default the daemon notifies you on the desktop, and the Slack watcher DMs you when a Slack-started session goes idle. When something else watches zen for you, such as a supervising agent session, you can send those notifications there instead.

```yaml
notify:
  desktop: false
  command: "~/bin/tell-lead"
  events: [review_requested, session_waiting, slack_task]
slack:
  completion_dm: false
```

- `notify.desktop` (default `true`): `false` turns off every desktop notification the daemon sends: new review request, worktree ready, PR updated, session waiting, and the digest. CLI commands are unaffected.
- `notify.command` (default unset): a shell command run with `sh -c` for each event whose type is listed in `notify.events`. It gets the event's JSON line (the same object as in the [event log](#event-log)) on stdin and the type in `ZEN_EVENT_TYPE`. It works whether or not `events.enabled` is on. Each run has a 10-second timeout; up to 4 run at once, in the background, so a slow command never holds up the daemon. A failure or timeout is logged in `watch.log` and otherwise ignored.
- `notify.events` (default `[review_requested, session_waiting, slack_task]`): any of the event types in the table above. An unknown name fails config loading. `[]` runs the command for nothing.
- `slack.completion_dm` (default `true`): `false` stops the Slack DM sent when a Slack-started session goes idle.

`session_waiting` follows the desktop notification's debounce (once per 5 minutes per session), so the command is not run on every scan.

## Environment

`ZEN_HOME` overrides the config/state directory (default `~/.zen`). Functional tests and a second daemon use this so they do not share `config.yaml` or `last_check.json` with a day-to-day install.

## State files

All state lives in `~/.zen/state/` (or `$ZEN_HOME/state/`):

| File | Purpose |
|------|---------|
| `watch.pid` | Daemon PID |
| `watch.log` | Daemon logs (rotated at 10MB; previous log kept as `watch.log.1`) |
| `last_check.json` | Last GitHub poll: timestamp, inbox count, `notified_new` (`repo:n` keys already announced), `applied_shas` (last-seen worktree HEAD) |
| `pr_cache.json` | PR titles/authors for display |
| `sessions.json` | Cached agent session states (updated every 10s by daemon) |
| `slack_seen.json` | Message keys already processed by the Slack task watcher |
| `slack_origins.json` | Worktree path → originating Slack thread, for the completion DM |
| `events.jsonl` | Event log, when `events.enabled` is true (rotated at 10MB; previous log kept as `events.jsonl.1`) |

## Upgrading

Install the new binary and restart the daemon so it is the process that actually polls GitHub. There is no migrate command and `config.yaml` does not need to change.

```bash
make build                 # or however you install zen
zen watch stop
zen watch start
```

Existing review worktrees stay in place. On the next poll they fast-forward if GitHub has moved and the tree is idle. In-flight reviews (local edits or a live agent) are left alone. You will not get a second “new review request” for PRs that were already in the inbox. If an author force-pushed while you were away, run `zen review <n>` — it asks before resetting.

`cleanup_after_days` can stay in `config.yaml`; it is ignored. After the restart, the first cleanup scan (one `cleanup_interval` later) removes every review whose PR merged more than `cleanup_after_merge` ago and passes the checks in [Merged-review cleanup](#merged-review-cleanup).

Older zen wrote `seen_prs` (PR numbers only) in `last_check.json`. The first poll after upgrade absorbs that list into `notified_new` and never writes `seen_prs` again.
