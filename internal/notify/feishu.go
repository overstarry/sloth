package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"time"

	"github.com/overstarry/sloth/internal/model"
)

// feishuChannel posts to a Feishu/Lark custom-bot webhook. Feishu (feishu.cn)
// and Lark (larksuite.com) share this implementation; only the webhook host
// differs, so the host is carried entirely in the configured URL.
type feishuChannel struct {
	name    string
	webhook string
	secret  string // optional HMAC signing secret
}

func (c *feishuChannel) Name() string { return c.name }

func (c *feishuChannel) Send(ctx context.Context, msg Message) error {
	card := c.renderCard(msg)
	payload := map[string]any{
		"msg_type": "interactive",
		"card":     card,
	}
	// Optional signature: sign with timestamp when a secret is configured.
	if c.secret != "" {
		ts := strconv.FormatInt(timeNow().Unix(), 10)
		sign, err := genSign(c.secret, ts)
		if err != nil {
			return err
		}
		payload["timestamp"] = ts
		payload["sign"] = sign
	}
	return postJSON(ctx, c.webhook, payload)
}

// renderCard builds an interactive card with a header colored by severity and a
// button linking back to the dashboard.
func (c *feishuChannel) renderCard(msg Message) map[string]any {
	elements := []any{
		map[string]any{
			"tag": "div",
			"text": map[string]any{
				"tag":     "lark_md",
				"content": msg.Summary + "\n" + msg.Detail,
			},
		},
	}
	if msg.Link != "" {
		elements = append(elements, map[string]any{
			"tag": "action",
			"actions": []any{map[string]any{
				"tag":  "button",
				"text": map[string]any{"tag": "plain_text", "content": "查看详情"},
				"url":  msg.Link,
				"type": "primary",
			}},
		})
	}
	return map[string]any{
		"header": map[string]any{
			"template": cardColor(msg.Level),
			"title":    map[string]any{"tag": "plain_text", "content": msg.Title},
		},
		"elements": elements,
	}
}

// cardColor maps severity to a Feishu card header template color.
func cardColor(l model.Level) string {
	switch l {
	case model.LevelCritical:
		return "red"
	case model.LevelWarn:
		return "orange"
	default:
		return "blue"
	}
}

// genSign computes the Feishu webhook signature: base64(HMAC-SHA256(key=secret,
// data="")) where the key is "timestamp\nsecret".
func genSign(secret, timestamp string) (string, error) {
	stringToSign := timestamp + "\n" + secret
	h := hmac.New(sha256.New, []byte(stringToSign))
	if _, err := h.Write([]byte("")); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

// timeNow is indirected for tests.
var timeNow = time.Now
