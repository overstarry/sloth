// Package notify delivers slow-SQL alerts to chat channels (WeCom / Feishu /
// Lark) through a pluggable, multi-channel registry with per-fingerprint
// cooldown and level-based routing.
package notify

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/overstarry/sloth/internal/config"
	"github.com/overstarry/sloth/internal/model"
)

// Message is a channel-agnostic alert payload. Each channel renders it into its
// own card/markdown format.
type Message struct {
	Title    string
	Level    model.Level
	Summary  string   // one-line: fingerprint, mean time, calls
	Detail   string   // markdown: root cause + suggestions
	Link     string   // dashboard detail URL
	Mentions []string // userids / phones, channel-specific
}

// Channel sends a rendered Message to one destination.
type Channel interface {
	Name() string
	Send(ctx context.Context, msg Message) error
}

// CooldownStore answers "when did we last successfully alert this fingerprint?"
// It is backed by notify_log so cooldown survives restarts.
type CooldownStore interface {
	LastNotified(ctx context.Context, fingerprint string) (time.Time, bool, error)
	RecordNotify(ctx context.Context, fingerprint, channel string, level model.Level, success bool, errMsg string) error
}

// Dispatcher routes messages to channels per the configured rules, enforcing
// cooldown and a token-bucket rate limit per channel.
type Dispatcher struct {
	enabled  bool
	cooldown time.Duration
	rules    config.NotifyRules
	channels map[string]Channel
	limiters map[string]*tokenBucket
	store    CooldownStore
	log      *slog.Logger
}

// NewDispatcher builds the channel registry from config.
func NewDispatcher(c config.NotifyConfig, store CooldownStore, log *slog.Logger) (*Dispatcher, error) {
	d := &Dispatcher{
		enabled:  c.Enabled,
		cooldown: c.Cooldown,
		rules:    c.Rules,
		channels: map[string]Channel{},
		limiters: map[string]*tokenBucket{},
		store:    store,
		log:      log,
	}
	for _, ch := range c.Channels {
		impl, err := buildChannel(ch)
		if err != nil {
			return nil, fmt.Errorf("channel %q: %w", ch.Name, err)
		}
		d.channels[ch.Name] = impl
		// WeCom caps bot messages at 20/min; use that as a safe shared default.
		d.limiters[ch.Name] = newTokenBucket(20, time.Minute)
	}
	return d, nil
}

func buildChannel(c config.ChannelConfig) (Channel, error) {
	switch c.Type {
	case "wecom":
		return &wecomChannel{name: c.Name, webhook: c.Webhook}, nil
	case "feishu", "lark":
		// Feishu and Lark share one implementation; only the webhook host differs.
		return &feishuChannel{name: c.Name, webhook: c.Webhook, secret: c.SignSecret}, nil
	default:
		return nil, fmt.Errorf("unknown channel type %q", c.Type)
	}
}

// Notify dispatches an alert for a diagnosed slow SQL. It is a no-op when
// notifications are disabled or the fingerprint is within its cooldown window.
func (d *Dispatcher) Notify(ctx context.Context, fingerprint string, msg Message) error {
	if !d.enabled {
		return nil
	}
	if d.inCooldown(ctx, fingerprint) {
		d.log.Debug("notify skipped: cooldown", "fingerprint", fingerprint)
		return nil
	}

	targets := d.routeFor(msg.Level)
	if len(targets) == 0 {
		return nil
	}
	for _, name := range targets {
		ch, ok := d.channels[name]
		if !ok {
			d.log.Warn("notify: unknown channel in rule", "channel", name)
			continue
		}
		d.limiters[name].wait(ctx)
		err := ch.Send(ctx, msg)
		if err != nil {
			d.log.Error("notify send failed", "channel", name, "err", err)
		}
		if d.store != nil {
			_ = d.store.RecordNotify(ctx, fingerprint, name, msg.Level, err == nil, errString(err))
		}
	}
	return nil
}

func (d *Dispatcher) inCooldown(ctx context.Context, fingerprint string) bool {
	if d.store == nil || d.cooldown <= 0 {
		return false
	}
	last, ok, err := d.store.LastNotified(ctx, fingerprint)
	if err != nil || !ok {
		return false
	}
	return time.Since(last) < d.cooldown
}

func (d *Dispatcher) routeFor(level model.Level) []string {
	switch level {
	case model.LevelCritical:
		return d.rules.Critical.Channels
	case model.LevelWarn:
		return d.rules.Warn.Channels
	default:
		return nil // info-level stays on the dashboard only
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
