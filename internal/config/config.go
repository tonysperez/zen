package config

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mgreau/zen/internal/agent"
	"github.com/mgreau/zen/internal/events"
	"gopkg.in/yaml.v3"
)

// Config holds the complete zen configuration.
type Config struct {
	Repos        map[string]RepoConfig `yaml:"repos"`
	WatchPaths   []string              `yaml:"watch_paths"`
	Authors      []string              `yaml:"authors"`
	PollInterval string                `yaml:"poll_interval"`
	Agent        string                `yaml:"agent"` // "claude" (default) or "codex"
	ClaudeBin    string                `yaml:"claude_bin"`
	CodexBin     string                `yaml:"codex_bin"`
	Terminal     string                `yaml:"terminal"` // "iterm", "ghostty", "kitty", or "macos"
	BranchPrefix string                `yaml:"branch_prefix"`
	IgnoreDrafts bool                  `yaml:"ignore_drafts"`
	Watch        WatchConfig           `yaml:"watch"`
	Slack        SlackConfig           `yaml:"slack"`
	Events       EventsConfig          `yaml:"events"`
	Notify       NotifyConfig          `yaml:"notify"`
}

// NotifyConfig routes the daemon's notifications. By default they go to the
// desktop only, as before. Command, when set, is run through `sh -c` for each
// event whose type is in Events, with the event JSON on stdin and
// ZEN_EVENT_TYPE in the environment, whether or not the event log is on.
type NotifyConfig struct {
	Desktop *bool    `yaml:"desktop"` // default true; false silences the daemon's desktop notifications
	Command string   `yaml:"command"` // default "": no command
	Events  []string `yaml:"events"`  // event types Command runs for; unset means DefaultNotifyEvents
}

// DefaultNotifyEvents are the event types notify.command runs for when
// notify.events is unset: the ones that ask for a person's attention.
var DefaultNotifyEvents = []string{
	events.TypeReviewRequested,
	events.TypeSessionWaiting,
	events.TypeSlackTask,
}

// DesktopEnabled reports whether the daemon sends desktop notifications,
// defaulting to true.
func (n NotifyConfig) DesktopEnabled() bool {
	return n.Desktop == nil || *n.Desktop
}

// GetEvents returns the event types notify.command runs for. Unset means
// DefaultNotifyEvents; an explicit empty list means none.
func (n NotifyConfig) GetEvents() []string {
	if n.Events == nil {
		return append([]string(nil), DefaultNotifyEvents...)
	}
	return n.Events
}

// wants reports whether notify.command runs for event type t.
func (n NotifyConfig) wants(t string) bool {
	if n.Command == "" {
		return false
	}
	for _, e := range n.GetEvents() {
		if e == t {
			return true
		}
	}
	return false
}

// EventsConfig controls the daemon's event log, <state dir>/events.jsonl: one
// JSON object per line for each review request, prepared or updated review,
// session state change, merged-review cleanup, and Slack pickup. Off unless
// Enabled is set.
type EventsConfig struct {
	Enabled bool `yaml:"enabled"`
}

// SlackConfig holds configuration for the Slack task watcher: polling for the
// user's own emoji reaction on a message, then spinning up a feature worktree
// seeded with the thread as context. Disabled unless Enabled is explicitly
// set to true. The Slack token itself is never stored here — it's read from
// the ZEN_SLACK_TOKEN environment variable at daemon startup.
type SlackConfig struct {
	Enabled      bool   `yaml:"enabled"`
	Emoji        string `yaml:"emoji"`         // reaction name (no colons) that flags a task, default "claudecode"
	DefaultRepo  string `yaml:"default_repo"`  // short repo name (from repos:) worktrees are created in
	PollInterval string `yaml:"poll_interval"` // default "5m"
	AckReaction  string `yaml:"ack_reaction"`  // reaction added on pickup, default "eyes"
	DoneEmoji    string `yaml:"done_emoji"`    // reaction (on the original message) or a merged PR from the worktree's branch stops further completion DMs, default "done_check"
	CompletionDM *bool  `yaml:"completion_dm"` // DM the user when a Slack-started session goes idle, default true
	Route        string `yaml:"route"`         // SlackRouteAgent (default) or SlackRouteEvent
}

// Slack routes: what the watcher does with a flagged thread.
const (
	// SlackRouteAgent creates a worktree in default_repo and launches the
	// agent on the thread.
	SlackRouteAgent = "agent"
	// SlackRouteEvent only emits a slack_task event (event log and
	// notify.command) for something else to pick up.
	SlackRouteEvent = "event"
)

// GetRoute returns the Slack route, defaulting to SlackRouteAgent.
func (s SlackConfig) GetRoute() string {
	if s.Route != "" {
		return s.Route
	}
	return SlackRouteAgent
}

// CompletionDMEnabled reports whether the daemon DMs the user when a
// Slack-started session goes idle, defaulting to true.
func (s SlackConfig) CompletionDMEnabled() bool {
	return s.CompletionDM == nil || *s.CompletionDM
}

// GetEmoji returns the configured reaction name, defaulting to "claudecode".
func (s SlackConfig) GetEmoji() string {
	if s.Emoji != "" {
		return s.Emoji
	}
	return "claudecode"
}

// GetAckReaction returns the reaction added on pickup, defaulting to "eyes".
func (s SlackConfig) GetAckReaction() string {
	if s.AckReaction != "" {
		return s.AckReaction
	}
	return "eyes"
}

// GetDoneEmoji returns the reaction name that marks a task done, defaulting
// to "done_check".
func (s SlackConfig) GetDoneEmoji() string {
	if s.DoneEmoji != "" {
		return s.DoneEmoji
	}
	return "done_check"
}

// PollIntervalDuration returns the Slack poll interval, defaulting to 5 minutes.
func (s SlackConfig) PollIntervalDuration() time.Duration {
	if s.PollInterval != "" {
		if d, err := time.ParseDuration(s.PollInterval); err == nil {
			return d
		}
	}
	return 5 * time.Minute
}

// WatchConfig holds configuration for the watch daemon's workqueue behavior.
type WatchConfig struct {
	DispatchInterval    string `yaml:"dispatch_interval"`     // default "10s"
	CleanupInterval     string `yaml:"cleanup_interval"`      // default "1h"
	SessionScanInterval string `yaml:"session_scan_interval"` // default "10s"
	CleanupAfterMerge   string `yaml:"cleanup_after_merge"`   // default "1h"
	Concurrency         int    `yaml:"concurrency"`           // default 2
	MaxRetries          int    `yaml:"max_retries"`           // default 5
	DigestInterval      string `yaml:"digest_interval"`       // "" = disabled, e.g. "2h"

	// Deprecated: merged-review cleanup is gated by CleanupAfterMerge. Still
	// parsed so existing config files load; the daemon no longer reads it.
	CleanupAfterDays int `yaml:"cleanup_after_days,omitempty"`
}

// DispatchIntervalDuration returns the dispatch interval as a time.Duration,
// falling back to the default of 10 seconds.
func (w WatchConfig) DispatchIntervalDuration() time.Duration {
	if w.DispatchInterval != "" {
		if d, err := time.ParseDuration(w.DispatchInterval); err == nil {
			return d
		}
	}
	return 10 * time.Second
}

// CleanupIntervalDuration returns the cleanup interval as a time.Duration,
// falling back to the default of 1 hour.
func (w WatchConfig) CleanupIntervalDuration() time.Duration {
	if w.CleanupInterval != "" {
		if d, err := time.ParseDuration(w.CleanupInterval); err == nil {
			return d
		}
	}
	return 1 * time.Hour
}

// CleanupAfterMergeDuration returns how long after a PR merges (GitHub's
// mergedAt) the daemon waits before removing its review worktree, defaulting
// to 1 hour. Zero removes it on the next cleanup scan. An unparsable or
// negative value falls back to the default.
func (w WatchConfig) CleanupAfterMergeDuration() time.Duration {
	if w.CleanupAfterMerge != "" {
		if d, err := time.ParseDuration(w.CleanupAfterMerge); err == nil && d >= 0 {
			return d
		}
	}
	return 1 * time.Hour
}

// GetCleanupAfterDays returns CleanupAfterDays with a default of 5.
//
// Deprecated: merged-review cleanup uses CleanupAfterMergeDuration.
func (w WatchConfig) GetCleanupAfterDays() int {
	if w.CleanupAfterDays > 0 {
		return w.CleanupAfterDays
	}
	return 5
}

// GetConcurrency returns the concurrency limit with a default of 2.
func (w WatchConfig) GetConcurrency() int {
	if w.Concurrency > 0 {
		return w.Concurrency
	}
	return 2
}

// GetMaxRetries returns the max retries with a default of 5.
func (w WatchConfig) GetMaxRetries() int {
	if w.MaxRetries > 0 {
		return w.MaxRetries
	}
	return 5
}

// DigestIntervalDuration returns the digest interval duration and whether it is enabled.
// An empty DigestInterval string disables the digest (returns 0, false).
func (w WatchConfig) DigestIntervalDuration() (time.Duration, bool) {
	if w.DigestInterval == "" {
		return 0, false
	}
	d, err := time.ParseDuration(w.DigestInterval)
	if err != nil || d <= 0 {
		return 0, false
	}
	return d, true
}

// SessionScanIntervalDuration returns the session scan interval as a time.Duration,
// falling back to the default of 10 seconds.
func (w WatchConfig) SessionScanIntervalDuration() time.Duration {
	if w.SessionScanInterval != "" {
		if d, err := time.ParseDuration(w.SessionScanInterval); err == nil {
			return d
		}
	}
	return 10 * time.Second
}

// RepoConfig holds per-repository configuration.
type RepoConfig struct {
	FullName string `yaml:"full_name"`
	BasePath string `yaml:"base_path"`
}

// Dir is the zen config and state directory. ZEN_HOME overrides the default
// ~/.zen so a test harness (or a second install) does not share last_check.json
// or config.yaml with a day-to-day daemon.
func Dir() string {
	if d := strings.TrimSpace(os.Getenv("ZEN_HOME")); d != "" {
		return d
	}
	return filepath.Join(os.Getenv("HOME"), ".zen")
}

func zenHome() string {
	return Dir()
}

// Load reads the YAML config from ~/.zen/config.yaml.
// Returns an error if the config file does not exist or is invalid.
func Load() (*Config, error) {
	yamlPath := filepath.Join(zenHome(), "config.yaml")
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		return nil, fmt.Errorf("config file not found: %s\nRun 'zen setup' to create it", yamlPath)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", yamlPath, err)
	}

	// Apply defaults for optional fields
	if cfg.PollInterval == "" {
		cfg.PollInterval = "5m"
	}
	if cfg.ClaudeBin == "" {
		cfg.ClaudeBin = "claude"
	}
	if cfg.CodexBin == "" {
		cfg.CodexBin = "codex"
	}
	if cfg.Agent == "" {
		cfg.Agent = "claude" // default for backward compatibility
	}
	if cfg.Agent != "claude" && cfg.Agent != "codex" {
		return nil, fmt.Errorf("invalid agent %q: must be \"claude\" or \"codex\"", cfg.Agent)
	}
	if cfg.Terminal == "" {
		cfg.Terminal = defaultTerminal()
	}
	switch cfg.Terminal {
	case "iterm", "ghostty", "kitty", "macos", "tmux":
	default:
		return nil, fmt.Errorf("invalid terminal type %q: must be \"iterm\", \"ghostty\", \"kitty\", \"macos\", or \"tmux\"", cfg.Terminal)
	}
	if cfg.Repos == nil {
		cfg.Repos = make(map[string]RepoConfig)
	}
	if err := cfg.validateRouting(); err != nil {
		return nil, err
	}

	cfg.expandPaths()
	return cfg, nil
}

// validateRouting rejects notify and Slack routing values zen does not know,
// so a typo fails at load instead of silently routing nothing.
func (c *Config) validateRouting() error {
	for _, t := range c.Notify.Events {
		if !events.IsType(t) {
			return fmt.Errorf("invalid notify.events entry %q: must be one of %s", t, strings.Join(events.Types(), ", "))
		}
	}
	switch c.Slack.Route {
	case "", SlackRouteAgent:
	case SlackRouteEvent:
		// With nowhere to send the event, every flagged thread would be
		// acked and marked seen, then dropped.
		if c.Slack.Enabled && !c.Events.Enabled && !c.Notify.wants(events.TypeSlackTask) {
			return fmt.Errorf("slack.route %q needs events.enabled: true, or notify.command with slack_task in notify.events", SlackRouteEvent)
		}
	default:
		return fmt.Errorf("invalid slack.route %q: must be %q or %q", c.Slack.Route, SlackRouteAgent, SlackRouteEvent)
	}
	return nil
}

// GetTerminal returns the configured terminal type.
func (c *Config) GetTerminal() string {
	return c.Terminal
}

// defaultTerminal is the terminal used when the config names none: tmux when
// zen runs inside a tmux session, such as on a workstation reached over ssh
// with no terminal application to drive, and iTerm otherwise, as before.
func defaultTerminal() string {
	if os.Getenv("TMUX") != "" {
		return "tmux"
	}
	return "iterm"
}

// AgentKind returns the effective agent kind. A non-empty override (e.g. from
// a --agent flag) wins over the configured default.
func (c *Config) AgentKind(override string) string {
	if override != "" {
		return override
	}
	if c.Agent != "" {
		return c.Agent
	}
	return "claude"
}

// NewAgent builds the configured agent, honouring an optional override kind.
func (c *Config) NewAgent(override string) agent.Agent {
	kind := c.AgentKind(override)
	bin := c.ClaudeBin
	if kind == "codex" {
		bin = c.CodexBin
	}
	return agent.New(agent.Kind(kind), bin)
}

// GetBranchPrefix returns the prefix for feature branch names.
// Falls back to git config user.name (with spaces replaced by hyphens), then empty string.
func (c *Config) GetBranchPrefix() string {
	if c.BranchPrefix != "" {
		return c.BranchPrefix
	}
	// Try git config user.name; replace spaces so the prefix is branch-safe.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "config", "user.name").Output()
	if err == nil {
		name := strings.ReplaceAll(strings.TrimSpace(string(out)), " ", "-")
		if name != "" {
			return name
		}
	}
	return ""
}

// expandPaths replaces ~ with $HOME in base paths.
func (c *Config) expandPaths() {
	home := os.Getenv("HOME")
	for name, repo := range c.Repos {
		if strings.HasPrefix(repo.BasePath, "~/") {
			repo.BasePath = filepath.Join(home, repo.BasePath[2:])
			c.Repos[name] = repo
		}
	}
}

// RepoNames returns all configured short repo names.
func (c *Config) RepoNames() []string {
	names := make([]string, 0, len(c.Repos))
	for name := range c.Repos {
		names = append(names, name)
	}
	return names
}

// RepoFullNames lists every configured repository as owner/repo, sorted so a
// poll queries them in a stable order. Repos with no full_name are skipped:
// they cannot be scoped in a GitHub search.
func (c *Config) RepoFullNames() []string {
	full := make([]string, 0, len(c.Repos))
	for _, repo := range c.Repos {
		if repo.FullName == "" {
			continue
		}
		full = append(full, repo.FullName)
	}
	sort.Strings(full)
	return full
}

// RepoFullName maps a short name to full GitHub owner/repo.
func (c *Config) RepoFullName(short string) string {
	if repo, ok := c.Repos[short]; ok {
		return repo.FullName
	}
	return short
}

// RepoShortName maps a full GitHub owner/repo to short name.
func (c *Config) RepoShortName(full string) string {
	for name, repo := range c.Repos {
		if repo.FullName == full {
			return name
		}
	}
	// Fallback: return last path component
	parts := strings.Split(full, "/")
	return parts[len(parts)-1]
}

// ConfiguredRepo returns the config short name for a GitHub owner/repo.
// Unlike RepoShortName, it does not fall back to the last path component, so
// an unconfigured repo is not mistaken for a configured one that happens to
// share a name.
func (c *Config) ConfiguredRepo(full string) (string, bool) {
	for name, repo := range c.Repos {
		if repo.FullName == full {
			return name, true
		}
	}
	return "", false
}

// RepoBasePath returns the local base path for a repo (the parent dir
// that contains the main clone directory).
func (c *Config) RepoBasePath(short string) string {
	if repo, ok := c.Repos[short]; ok {
		return repo.BasePath
	}
	return ""
}

// AllBasePaths returns all configured repo base paths.
func (c *Config) AllBasePaths() []string {
	paths := make([]string, 0, len(c.Repos))
	for _, repo := range c.Repos {
		paths = append(paths, repo.BasePath)
	}
	return paths
}

// IsAuthor returns true if the given login is in the authors list.
func (c *Config) IsAuthor(login string) bool {
	for _, a := range c.Authors {
		if a == login {
			return true
		}
	}
	return false
}

// StateDir returns the path to the zen state directory.
func StateDir() string {
	return filepath.Join(zenHome(), "state")
}

// EventsPath returns the path to the event log, <state dir>/events.jsonl.
func EventsPath() string {
	return events.Path(StateDir())
}

// EventOptions says where the daemon sends an event under this config.
func (c *Config) EventOptions() events.Options {
	return events.Options{
		Log:          c.Events.Enabled,
		StateDir:     StateDir(),
		Command:      c.Notify.Command,
		CommandTypes: c.Notify.GetEvents(),
	}
}

// EnsureDirs creates required zen directories.
func EnsureDirs() error {
	dirs := []string{
		StateDir(),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}
