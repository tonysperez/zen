# Configuration

Zen reads `~/.zen/config.yaml`. Run `zen setup` for an interactive walkthrough, pass it flags for an [unattended install](#unattended-install), or edit the file directly.

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
remote_control: false  # launch every session with remote control on (see "Remote control")

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
  route: agent           # "agent" (default): worktree + agent; "event": notify and log a slack-task only
  poll_interval: "5m"
  ack_reaction: eyes      # reaction added on pickup, default "eyes"
  client_id: "123.456"    # Slack app client ID, for zen slack login
  redirect_port: 8765     # localhost port for the sign-in redirect, default 8765
  completion_dm: true     # DM you when a Slack-started session goes idle, default true
  lead_channel: ""        # channel or DM ID a lead agent session talks to you in; see "Lead channel"
  lead_poll_interval: "30s"
  lead_prefix: ":robot_face: "
  lead_notify: none       # what zen slack post --notify does: none, command or reminder

# What each new tmux session gets, with terminal: tmux. See "New tmux sessions".
tmux:
  first_window_command: zen inbox
  agents_window: false
  agents_window_dir: ~/git
  session: ""            # open worktree windows in this session instead (see "Terminal")

# Run a command for each daemon notification, e.g. to push it to your phone.
# See "Push notifications" below.
notify:
  command: 'curl -fsS -d "$ZEN_MESSAGE" https://ntfy.sh/<random-topic>'
  events: [session-waiting]
  desktop: true          # false: only the command, no desktop notifications

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

### Remote control

`remote_control: true` (default `false`) launches every agent session zen starts or resumes — `zen review`, `zen work`, the resume commands, and Slack-triggered sessions — so it can be driven from another device:

| | `claude` | `codex` |
|--|----------|---------|
| How | `claude --remote-control …` | zen brings up Codex's app-server daemon with remote control, then launches `codex --remote unix:// -C <worktree> …` |
| Scope | per session | one daemon shared by all sessions; each TUI attaches to it |

Codex has no per-session switch: remote control belongs to its (experimental) app-server daemon. Right before opening each tab, zen runs `codex app-server daemon enable-remote-control` and `codex app-server daemon start` (both no-ops when already done), with their output appended to `~/.zen/state/codex-daemon.log`. The daemon runs from `/` in its own session, so it outlives the tab that started it and never makes a worktree look busy; `-C <worktree>` keeps each session in its own worktree. zen resolves `codex_bin` to an absolute path (expanding `~/`) and uses it both to start the daemon and to launch the session; this needs `codex_bin` to name an executable, not a shell command like `npx codex`. If the daemon cannot be brought up, zen prints a warning and launches Codex without remote control. Commands printed by `--no-iterm` never start the daemon, so they launch Codex locally.

Things to know about the shared Codex daemon:

- **Environment.** Codex runs tools inside the daemon, which keeps the environment (`PATH`, tool-manager shims, credentials) of whichever process started it — the first `zen` command or `zen watch` that launched a Codex session — not the terminal each session opens in. If `zen watch` runs with a minimal `PATH` (e.g. under launchd), start the daemon yourself from a full shell with `codex app-server daemon start` before the first Slack-triggered session, or restart it with `codex app-server daemon restart`.
- **Upgrades.** A daemon that is already running is left alone, so after upgrading Codex it keeps running the old build while new sessions use the new one. Run `codex app-server daemon restart` after an upgrade.
- **Session detection.** A remote Codex thread runs inside the daemon, not in the terminal tab. zen detects a live session by its process in the worktree, so a thread that keeps running after its tab closes (for example, driven from a paired device) shows as stopped, and the worktree counts as idle for cleanup and fast-forward.
- **Persistence.** `enable-remote-control` is a Codex setting that outlives zen, and zen re-applies it on every Codex launch while `remote_control` is on. Turning `remote_control` off in zen does not turn it off in Codex; run `codex app-server daemon disable-remote-control` (or `... stop`) for that. Pair a device with `codex remote-control pair`.

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
reports an error; use `--no-terminal` there instead, or set `tmux.session`.

To collect sessions in one place, name a tmux session:

```yaml
tmux:
  session: workers
```

New windows then open in `workers` instead of the session zen runs in, in the
background (zen never switches your client to them), and zen creates the
session detached if it does not exist (`tmux attach -t workers` to look). This
works outside tmux too, which covers the daemon and scripts. An existing window
for the worktree is still reused wherever it is. When `terminal` is unset,
setting `tmux.session` makes it `tmux`. The name may not contain `:` or `.`.

zen tags each window it opens with the window option `@zen-worktree`, so one
worktree gets one window. Opening a worktree that already has a window, as
`zen review resume` or `zen work resume` do, reuses it. If the window is at its
shell prompt, the agent starts there. If something is still running in it, or
you split it into several panes, zen only switches to it and says so. Windows the daemon opens for Slack tasks open in the
background, so they don't take focus from the window you're typing in.

### Highlighting windows that need you

zen keeps the window option `@zen-status` on each of its windows. The value is
`running`, `waiting` (the agent needs you), or `stopped`. Two things set it:

- For Claude, hooks that zen passes with `--settings` when it opens the agent
  in a tmux window. They set the status as soon as Claude starts, finishes a
  turn or asks for a permission, and when you reply, and mark it `stopped`
  when Claude exits. Nothing is written into
  the worktree, your own hooks still run, and the commands zen prints for
  `--no-terminal` don't include them.
- The daemon's session scan. This works for every agent, after a delay of up
  to `session_scan_interval` plus the idle threshold. Where Claude's hooks set
  the status, the daemon marks the window `stopped` once Claude exits, and
  `waiting` after a turn you interrupted with Esc, which no hook reports. It
  tells that from a long, quiet tool by the interrupt marker Claude records
  in the session. A session idle for 10 minutes is marked `waiting` whatever
  the hooks last said, for waits no hook reports, unless its last step is a
  tool call still running, such as a subagent.

After you approve a permission, the window stays `waiting` until the
approved tool finishes: no hook fires on the approval itself.

The daemon reaches tmux the way any tmux client does: through `$TMUX` if it
was started inside tmux, else the default server. If you run tmux on a custom
socket (`tmux -L` or `-S`), start the daemon from inside that tmux, or it
won't find the windows to update.

tmux doesn't style windows by user options on its own: zen's tmux settings
do that, as described in [tmux settings](#tmux-settings).

### tmux settings

`zen setup tmux` installs zen's tmux settings, and `zen setup` offers to when
you pick tmux. They live in a file of zen's own, `~/.zen/tmux.conf`, which
zen rewrites each time, so don't edit it. Your tmux config (the first of
`~/.tmux.conf`, `$XDG_CONFIG_HOME/tmux/tmux.conf` and
`~/.config/tmux/tmux.conf` that exists, as tmux looks for it, else
`~/.tmux.conf`) gets one line that reads it, between `# >>> zen >>>` and `# <<< zen <<<` lines.
Nothing else in your file is changed. Run inside tmux, setup reloads it.

The settings:

- `focus-events on`, so zen doesn't notify you about the window you're
  looking at.
- A reverse `● ` in front of windows whose agent is waiting on you, added
  to the front of your own `window-status-format`, or tmux's default. zen's
  line comes last in your config, so it sees a format your config sets
  above it. If a theme sets the format later, put zen's marker in front of
  it yourself: `#{?#{==:#{@zen-status},waiting},#[reverse]● ,}`.
- `prefix + W`: a picker of just the waiting windows.
- A `session-created` hook that sets up each new session, as below. It uses
  hook index 100, so your own `session-created` hooks are kept.

### New tmux sessions

Each new tmux session gets, as `config.yaml` says:

```yaml
tmux:
  first_window_command: zen inbox  # the default; "" types nothing
  agents_window: false             # the default; true opens an Agents window
  agents_window_command: claude    # default: claude agents, or codex agents
  agents_window_dir: ~/git         # the default
```

- `first_window_command` is typed into the session's first window, so the
  window stays an ordinary prompt afterwards: `zen inbox` shows what needs
  you, `zen dashboard` everything you have going. It's only typed into a
  session you open from a terminal (`tmux new`, `tmux new -A`) that is a lone
  shell. Sessions a script or tool creates in the background (`tmux new -d`)
  are left alone, since the script may type into them itself; so are a
  session created to run a program, such as `tmux new 'vim'`, and one grouped
  with another (`tmux new -t other`), whose windows someone may be using.
- `agents_window` opens a window named `Agents`, in the background, running
  `agents_window_command` through your login shell. By default that's your
  agent's view of its sessions: `claude agents`, or `codex agents` with
  `agent: codex`. Set it to `claude` for a plain session instead. The shell
  stays open if the command exits.
- `agents_window_dir` is where the Agents window starts. It defaults to
  `~/git`, rather than your home directory, so Claude doesn't ask whether to
  trust your home directory each time. If the directory doesn't exist, the
  window starts in the session's directory.

`zen setup` asks for these when you pick tmux. The hook reads `config.yaml`
each time, so a change applies to sessions created after it, without
running setup again. Sessions that already exist, such as one you reattach
to with `tmux new -A`, are left as they are. The hook's output would show in
the new session, so problems go to `~/.zen/state/tmux-hook.log` instead. This applies to every new
session on the tmux server, not only zen's.

### Desktop notifications over ssh

When you ssh into the host from a Mac, zen sends a desktop notification for
the Claude in each of its windows, through tmux and ssh to iTerm2 or Ghostty:

- When Claude finishes a turn: the start of its reply, right away. A turn
  that ends on an error, such as a rate limit, says so.
- When Claude asks for a permission or an answer: its own text, such as
  "Claude needs your permission to use Bash".

The title names the window's worktree, such as `Claude · app-fix-login`, or
for a PR review its repo, number, and title, such as
`Claude · app#1042 · Add pagination to ListUsers`.

zen does this with its own hook (`zen hook notify`), passed with the other
hooks. It turns off Claude's own notifications for those sessions only, so
you don't get each one twice; your Claude settings are not changed. With
tmux's `focus-events` on, as `zen setup tmux` sets it, there's no notification
for the window you're looking at in a focused terminal. Claude's reply can
quote untrusted text, so zen drops control and invisible formatting
characters from it before it reaches your terminal.

```yaml
notify:
  terminal: true            # false: no zen notifications; Claude keeps its own
  terminal_events: [stop, stop_failure, permission_prompt, elicitation_dialog]   # the default
  terminal_sequence: auto   # or osc777 (Ghostty), osc9 (iTerm2 and others)
```

`terminal_events` takes `stop` (a turn ended), `stop_failure` (a turn ended on
an error), and Claude's notification types, such as `permission_prompt`,
`elicitation_dialog`, or `idle_prompt`. A value zen doesn't know is kept, in
case Claude has added a type since, but `zen setup tmux` and the daemon log
warn about it, since a misspelt one would quietly drop its notifications. An
empty list turns them all off. `auto` sends OSC 777, which has a separate title,
when every terminal attached to the session is Ghostty, and OSC 9, which puts
the title in front of the text, otherwise.

These notifications get through tmux because zen sets `allow-passthrough all`
on each window it opens. With `all`, tmux lets them through from windows you
aren't looking at too. tmux 3.3 only knows `on`, which covers the visible
window only, and older tmux has no passthrough. Passthrough gives programs in
zen's windows the same access to your terminal they would have without tmux.

On the Mac, the terminal has to show them:

- iTerm2: turn on Settings → Profiles → Terminal → *Notification Center
  Alerts*, and under *Filter Alerts* tick *Send escape sequence-generated
  alerts*.
- Ghostty shows them by default. It uses its window title as the subtitle;
  set `title = <name>` in Ghostty's config for a fixed one.
- Either way, the terminal app needs notification permission in System
  Settings.

This only works while a tmux client is attached over ssh. To be notified
while you're detached, use a [push command](#push-notifications).

## Push notifications

The daemon shows its notifications (a session waiting on you, a review ready,
and so on) on the desktop of the host it runs on. On a remote host that is
nowhere you would see them. `notify.command` is a shell command the daemon
also runs for each notification, for example a curl to a push service with
apps for the Mac and the phone:

```yaml
notify:
  # Use a long random topic: anyone who knows it can read the messages.
  command: 'curl -fsS -H "Title: $ZEN_TITLE" -d "$ZEN_MESSAGE" https://ntfy.sh/zen-k3v9q2xw7'
  events: [session-waiting]   # optional; empty means every event
```

The command runs through `/bin/sh` with the notification in its environment.
Quote these variables, as above, rather than pasting them into the command:

| Variable | Contents |
|---|---|
| `ZEN_EVENT` | `pr-review`, `pr-updated`, `worktree-ready`, `pr-merged`, `stale-worktrees`, `session-waiting`, `digest`, `slack-task`, `slack-message`, or `lead-message` |
| `ZEN_TITLE` | The notification title |
| `ZEN_MESSAGE` | The notification text |
| `ZEN_SUBTITLE` | The subtitle, such as the model or the repo; may be empty |
| `ZEN_RESUME` | The zen command that opens the worktree, for `session-waiting` and `worktree-ready` |
| `ZEN_URL` | `slack-task`, `slack-message` and `lead-message` only: the thread's or message's permalink |
| `ZEN_TEXT` | `slack-task` and `slack-message` only: the thread as `user: text` paragraphs, or the message, cut to 4,000 bytes |
| `ZEN_EVENT_JSON` | `slack-task` and `slack-message` only: the whole event as it appears in the [event log](#event-log), text up to 20,000 bytes |

`ZEN_EVENT` can also be `slack-task`, sent with `slack.route: event` (see
[Handing tasks to something else](#handing-tasks-to-something-else-slackroute-event)),
or `slack-message` and `lead-message` from the [lead channel](#lead-channel).
Those two only run the command; they are never shown on the desktop.

`events` limits the command to those events. A command that fails, or runs
longer than 30 seconds, is logged in the daemon log (`zen watch logs`). The
desktop notification is still sent either way.

`notify.desktop: false` turns the desktop notifications off and leaves only the
command, for when something other than you should get them, such as a
supervising agent session. It covers every desktop notification the daemon
sends, the Slack sign-in alert included. It defaults to `true`.

```yaml
notify:
  desktop: false
  command: '~/bin/tell-lead'
  events: [pr-review, session-waiting]
slack:
  completion_dm: false   # and no Slack DM when a Slack-started session goes idle
```

## Event log

With `events.enabled: true`, the daemon appends one JSON object per line to `~/.zen/state/events.jsonl` (or `$ZEN_HOME/state/events.jsonl`) whenever something happens that a supervisor, such as another agent session, might act on. A watcher can tail that one file instead of polling `zen inbox` and the worktrees. The log is off by default, and turning it on changes nothing else the daemon does.

Each line has `time` (RFC3339, UTC) and `type`, plus whichever of these apply: `repo` (short name from `repos:`), `pr`, `worktree` (path), `session` (agent session ID), `sha`, `url`, and a small `data` object. Types use the same names as the [push notification](#push-notifications) events where there is one, so `$ZEN_EVENT` and the log agree.

| Type | When | Notable fields |
|------|------|----------------|
| `pr-review` | First sighting of a review request with no local worktree (with the "New PR Review Request" notification) | `repo`, `pr`, `sha`, `url`; `data.title`, `data.author`, `data.draft`, `data.auto_create` (author is in `authors:`) |
| `worktree-ready` | The daemon created a review worktree | `repo`, `pr`, `worktree`, `sha`, `url`; `data.title`, `data.author` |
| `pr-updated` | A review worktree was fast-forwarded to a new GitHub head | `repo`, `pr`, `worktree`, `sha`; `data.old_sha`, `data.new_sha` |
| `session-waiting` | An agent session went from running to waiting for input (once per 5 minutes per session, like the notification) | `worktree`, `session`, `pr` for reviews; `data.name`, `data.kind`, `data.model`, `data.resume` |
| `session-stopped` | A running or waiting session's process exited (log only, no notification) | as `session-waiting` |
| `review-cleaned` | Merged-review cleanup removed a review worktree (log only; `pr-merged` is the notification that a PR merged, which the daemon doesn't send today) | `repo`, `pr`, `worktree`, `sha` (PR head), `url`; `data.merged_at` |
| `slack-task` | The Slack watcher picked up a flagged message | `url` (permalink); `data.channel`, `data.ts`, `data.permalink`, `data.user` (thread author), `data.flagged_by`, `data.text` (thread, up to 20,000 bytes), `data.route`; with `route: agent` also `repo`, `worktree`, `data.branch` |
| `slack-message` | You wrote a message, top-level or in a thread, in `slack.lead_channel` (with a `slack-message` push) | `url` (permalink); `data.channel`, `data.ts`, `data.thread_ts` (empty for a top-level message), `data.text` (up to 20,000 bytes; `data.truncated` when cut), `data.user` |
| `lead-message` | `zen slack post` posted to the lead channel (log only; `--notify` with `lead_notify: command` sends the `lead-message` push) | `url` (permalink); `data.channel`, `data.ts`, `data.thread_ts`, `data.text` (as posted, prefix included) |

Example:

```json
{"time":"2026-01-02T15:04:05Z","type":"pr-review","repo":"app","pr":42,"sha":"3f2c…","url":"https://github.com/octo-sts/app/pull/42","data":{"author":"wlynch","auto_create":true,"draft":false,"title":"Fix token refresh"}}
```

Each line is written with a single `O_APPEND` write, so a reader never sees half a line. At 10MB the log is renamed to `events.jsonl.1` (replacing the previous backup) and a new one is started, as `watch.log` is.

Read it with `zen events`:

```bash
zen events                                   # everything, oldest first (backup, then current)
zen events --since 2h                        # last two hours; also takes an RFC3339 time
zen events --type pr-review,slack-task
zen events --since 30m --follow              # keep printing new events; survives rotation
zen events --json                            # one JSON array instead of lines
```

Output is the raw JSON lines, so it pipes into `jq`. `--follow` always streams lines.

## Slack task watcher

An opt-in daemon loop that polls for the user's own emoji reaction (`slack.emoji`, default `claudecode`) via `reactions.list`, then for each new hit: adds an ack reaction in-thread (`slack.ack_reaction`, default `eyes`), fetches the thread, creates a feature worktree in `slack.default_repo` from `origin/main`, and immediately launches the agent in a terminal tab with the thread as the initial prompt — the launched session does all the "figure out what to do" work, not the daemon. Unlike the PR-review flow, this does open a terminal tab automatically: a Slack reaction is a deliberate, comparatively rare signal, not the high-volume PR-review firehose.

When that worktree's session later goes idle ("waiting", the same signal that drives the desktop `notify.SessionWaiting`), the daemon also sends a Slack DM to the user with the worktree name, resume command, and a link back to the original thread. That DM stops once the task is actually done: either a merged PR from the worktree's branch, or the configured `slack.done_emoji` (default `done_check`) reacted onto the original message. `slack.completion_dm: false` (default `true`) turns that DM off.

Setup:

1. `slack.enabled: true` in config, plus `slack.default_repo` (required for the default `route: agent` — no hardcoded default).
2. Create a Slack app (a dedicated personal one is recommended over reusing a shared org bot) with these user token scopes: `reactions:read`, `channels:history`, `groups:history`, `im:history`, `mpim:history`, `chat:write`, and `reminders:write` (only used by `slack.lead_notify: reminder`; see [Lead channel](#lead-channel)).
3. Give zen a token, in one of two ways:
   - **Sign in with OAuth (recommended).** In the app's OAuth settings, turn on PKCE and add the redirect URL `http://localhost:8765/callback`. Put the app's client ID in `slack.client_id`, then run `zen slack login`. No client secret is needed. Turning on PKCE can't be undone from the app settings; reverting it takes a request to Slack support.
   - **Paste a token.** Export a user token as `ZEN_SLACK_TOKEN` in the environment the daemon starts in (`zen watch start` inherits your shell's env). When set, it takes precedence over a stored sign-in.

If `slack.enabled` is true but no token is available, or `auth.test` fails at startup, the daemon logs a warning and runs with the Slack watcher disabled rather than failing to start.

### Handing tasks to something else: `slack.route: event`

`slack.route: agent` (the default) is the behaviour above. With `slack.route: event`, pickup is the same up to the point of acting: the daemon adds the ack reaction, marks the message seen, and fetches the thread. Then, instead of creating a worktree and launching an agent, it sends a `slack-task` notification (desktop, and `notify.command` when `notify.events` allows it, with the thread in `$ZEN_URL`, `$ZEN_TEXT` and `$ZEN_EVENT_JSON`; see [Push notifications](#push-notifications)) and, with `events.enabled`, logs a `slack-task` event. A supervisor, such as a lead agent session, decides what to do with it. No worktree is recorded for the thread, so no completion DM follows. With `route: agent`, the `slack-task` event is still logged when the event log is on, but no notification is sent: the agent opening is the notification.

`slack.default_repo` is only required for `route: agent`. With `route: event` and `slack.enabled: true`, config loading fails if the task has nowhere to go: `notify.desktop: false`, `events.enabled` off, and no `notify.command` that takes `slack-task`. Any other `route` value fails config loading.

```yaml
slack:
  enabled: true
  route: event
notify:
  desktop: false
  command: '~/bin/tell-lead'   # reads $ZEN_EVENT_JSON
  events: [slack-task, session-waiting]
```

### Lead channel

A lead agent session that runs your other sessions can talk to you through one Slack conversation: it posts status updates and questions there, and you answer or start requests by writing there. Set `slack.lead_channel` to that conversation's ID: a private channel only you are in, or your own DM with yourself (open it in Slack and copy the ID, `C…`/`G…` or `D…`, from the conversation details). A `#name` is refused.

```yaml
slack:
  enabled: true
  lead_channel: C0123456789
  lead_notify: reminder    # optional
notify:
  command: '~/bin/tell-lead'   # reads $ZEN_EVENT_JSON
  events: [slack-message, slack-task, session-waiting]
events:
  enabled: true
```

**Writing to the lead.** With `slack.enabled` and a sign-in, the daemon reads the channel every `slack.lead_poll_interval` (default `30s`, at least `5s`) with `conversations.history`, and reads a thread with `conversations.replies` when it has new replies. Threads stay watched for 7 days after their first message; at most 10 threads are read per poll, and the rest on the next one. Each new message you wrote there, top-level or a thread reply, becomes a `slack-message`: logged in the [event log](#event-log) with `events.enabled`, and pushed through `notify.command` when `notify.events` allows it (`$ZEN_URL`, `$ZEN_TEXT`, `$ZEN_EVENT_JSON`; never a desktop notification, since you just wrote it). Messages from anyone else, bots and join notices are ignored. The first poll after `lead_channel` is set, or changed, starts from that moment: nothing earlier is replayed. Cursors live in `slack_lead.json`. Reacting to any message with the task emoji still works as before; with `slack.route: event` it becomes a `slack-task`.

**The lead writing to you.** The lead posts with `zen slack post`, which can only post to `slack.lead_channel`. It has no flag or argument that names a channel, so allowing a session to run it does not let it message anyone else, which a permission rule on a general Slack tool can't promise:

```bash
zen slack post "PR 42 is green. Merge it?"          # prints {"ts": ..., "permalink": ...}
echo "long status" | zen slack post -                # text from standard input (also with no argument)
zen slack post --thread 1700000000.123456 "Merged."  # reply in a thread
zen slack post --notify "Need a decision on PR 42"   # and alert you (slack.lead_notify)
```

zen's sign-in is a user token, so zen's posts appear as you. Two things follow. First, the daemon must not read the lead's posts back as your requests: every post starts with `slack.lead_prefix` (default `:robot_face: `), and its timestamp is recorded in `slack_lead.json`; a message with a recorded timestamp, or whose text starts with the prefix, is never a `slack-message`. Don't start your own messages with the prefix. Second, Slack doesn't notify you about your own messages, so a post alone makes no sound. `--notify` adds an alert, as `slack.lead_notify` says:

| `lead_notify` | `--notify` does |
|---|---|
| `none` (default) | Nothing. |
| `command` | Runs `notify.command` as a `lead-message` notification: `$ZEN_TITLE` is `Lead`, `$ZEN_MESSAGE` the post's first line, `$ZEN_URL` its permalink. Config loading fails if `lead_channel` is set and no `notify.command` takes `lead-message`. |
| `reminder` | Sets a Slack reminder for you, due in about a minute, with the text `Lead: <first line> <permalink>`. Slackbot delivers it as a DM, which Slack does notify you about. Needs the `reminders:write` user scope: add it to your Slack app and run `zen slack login` again (sign-ins from before zen asked for it lack it). Without the scope, it falls back to `command` when `notify.command` takes `lead-message`, and otherwise prints a warning. |

An alert that fails never fails the post. `zen slack post` fails with a clear message when `slack.lead_channel` is unset or there is no sign-in (`zen slack login`, or `ZEN_SLACK_TOKEN`); it does not need `slack.enabled` or `slack.default_repo`. With `events.enabled`, each post is also logged as a `lead-message` event.

| Key | Default | Meaning |
|---|---|---|
| `slack.lead_channel` | unset (off) | Channel or DM ID the lead and you talk in |
| `slack.lead_poll_interval` | `30s` | How often the daemon reads it (at least `5s`); read at daemon start |
| `slack.lead_prefix` | `:robot_face: ` | Put before every `zen slack post` message; marks zen's posts |
| `slack.lead_notify` | `none` | What `--notify` does: `none`, `command` or `reminder` |

`lead_channel` and `lead_prefix` take effect on the daemon's next config reload; `lead_poll_interval` needs `zen watch stop && zen watch start`.

### Signing in with `zen slack login`

`zen slack login` prints the Slack sign-in URL (and opens it when a browser is available), then waits for Slack to redirect back to `http://localhost:8765/callback`. It stores the resulting token in `slack_token.json` in the state directory, readable only by you.

On a remote workstation, the browser runs on your laptop, so the redirect goes to the laptop's `localhost`. Either works:

- **Paste-back:** the browser lands on a page that fails to load. Copy its URL from the address bar and paste it into the waiting `zen slack login`.
- **Port forwarding:** connect with `ssh -L 8765:localhost:8765` (for `cg work ssh`, put `-L 8765:localhost:8765` after the node name), and zen receives the redirect directly.

Use `--port` (or `slack.redirect_port`) for a different port; the redirect URL registered in the Slack app must match it. `--no-listen` skips the local listener and only accepts a pasted URL.

Access tokens last 12 hours, and zen refreshes them automatically, saving each rotated refresh token. Refresh tokens for PKCE apps expire 30 days after sign-in, so run `zen slack login` again about once a month. `zen slack status` shows the date. If a refresh fails while the daemon runs, it logs an error and sends one desktop alert; running `zen slack login` again fixes it without restarting the daemon. `zen slack logout` revokes and deletes the stored token.

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
| `slack_token.json` | Rotating Slack OAuth token from `zen slack login` (mode 0600) |
| `slack_lead.json` | Lead channel cursors, messages already reported, and the timestamps of `zen slack post` messages |
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

## Unattended install

`zen setup` runs without prompting when given the config as flags or as a file, for machine provisioning (cloud-init, Ansible, dotfiles, a Nix activation script):

```bash
zen setup \
  --repo 'app=octo-sts/app:~/git/repo-octo-sts-app' \
  --repo 'wolfi-dev/os:~/git/repo-os' \
  --author mattmoor,wlynch \
  --terminal tmux

# or install a config file you manage yourself (comments are kept)
zen setup --config-file ./zen-config.yaml
generate-config | zen setup --config-file -
```

| Flag | Meaning |
|------|---------|
| `--repo [short=]owner/name:base_path` | A repository (repeatable). `short` defaults to `name`. Quote the value to keep `~` literal; zen expands it when it loads the config. |
| `--author <login>` | GitHub username for PR filtering (repeatable or comma-separated) |
| `--agent claude\|codex` | Coding agent, default `claude` |
| `--terminal iterm\|ghostty\|kitty\|macos\|tmux` | When omitted, zen picks at runtime: `tmux` inside a tmux session, `iterm` otherwise |
| `--branch-prefix <prefix>` | Feature branch prefix, default git `user.name` |
| `--config-file <path>` | Install this file (`-` for stdin) instead of building one from flags. Cannot be combined with the flags above. |
| `--force` | Replace an existing config that differs |
| `--skip-prompts` | Do not install the agent's slash-command prompts |
| `--non-interactive` | Never prompt, even with no other flags (fails instead) |

Behaviour suited to provisioning:

- **Idempotent.** If `~/.zen/config.yaml` already has the same content, setup leaves it alone and exits 0. If the content differs, setup exits 1 without touching it, unless you pass `--force`. Slash-command prompts that are already installed are kept.
- **Validated up front.** The config is checked with the rules `zen` uses to load it. It also needs at least one repo whose short name is letters, digits, `.`, `_` or `-` (it names worktree directories), with an `owner/name` `full_name` and a `base_path`. Unknown keys such as a misspelt `authers:` are errors too, and so is a file with more than one YAML document (`---`), since zen reads only the first. A bad value fails the install rather than the first command after it. Interactive `zen setup` applies the same rules and asks again on a bad answer.
- **Atomic.** The file is written to a temporary file, flushed to disk and renamed, so a running daemon never reads a partial config and a crash right after provisioning cannot leave an empty one. If `~/.zen/config.yaml` is a symlink (stow, chezmoi, home-manager), setup writes to the link's target, even one that does not exist yet, and keeps the link and the file's permissions.
- **No hang without a terminal.** Interactive `zen setup` with stdin closed (for example `</dev/null`) exits with an error instead of waiting forever.
- **Run it as the user who runs zen.** Setup writes `~/.zen` and the agent's prompts under `$HOME`, and the files it creates belong to whoever runs it. Provisioning that runs as root should switch to the target user first: `sudo -u alice -H zen setup ...`, or `become_user: alice` in Ansible. Pointing `HOME` or `ZEN_HOME` at another user's home from root leaves root-owned files there that the user's daemon cannot write.
- `ZEN_HOME` moves the config and state directory away from `~/.zen`, for example to test a config without touching your own.

Setup does not authenticate GitHub or start the daemon. Run `gh auth login --with-token` (or set `GH_TOKEN`) and `zen watch start` as separate provisioning steps.
