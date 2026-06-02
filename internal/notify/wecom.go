package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/overstarry/sloth/internal/model"
)

// wecomChannel posts to a WeCom (企业微信) group-bot webhook using markdown.
type wecomChannel struct {
	name    string
	webhook string
}

func (c *wecomChannel) Name() string { return c.name }

func (c *wecomChannel) Send(ctx context.Context, msg Message) error {
	content := renderMarkdown(msg)
	// WeCom markdown does not support @ inside the body; mentions ride along
	// as a trailing line of <@userid> tokens which the client resolves.
	for _, m := range msg.Mentions {
		content += fmt.Sprintf("\n<@%s>", m)
	}
	payload := map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]string{"content": content},
	}
	return postJSON(ctx, c.webhook, payload)
}

// renderMarkdown builds the shared markdown body used by both channels.
func renderMarkdown(msg Message) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "**%s %s**\n", levelEmoji(msg.Level), msg.Title)
	fmt.Fprintf(&b, "%s\n\n", msg.Summary)
	if msg.Detail != "" {
		fmt.Fprintf(&b, "%s\n", msg.Detail)
	}
	if msg.Link != "" {
		fmt.Fprintf(&b, "\n[查看详情](%s)", msg.Link)
	}
	return b.String()
}

func levelEmoji(l model.Level) string {
	switch l {
	case model.LevelCritical:
		return "🔴"
	case model.LevelWarn:
		return "🟠"
	default:
		return "🔵"
	}
}

// postJSON sends a JSON body and treats a non-2xx or errcode!=0 as failure.
func postJSON(ctx context.Context, url string, payload any) error {
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		var b bytes.Buffer
		_, _ = b.ReadFrom(resp.Body)
		return fmt.Errorf("http %d: %s", resp.StatusCode, b.String())
	}
	var r struct {
		Code int    `json:"errcode"`
		Msg  string `json:"errmsg"`
		// Feishu uses "code" rather than "errcode".
		FCode int    `json:"code"`
		FMsg  string `json:"msg"`
	}
	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(resp.Body)
	_ = json.Unmarshal(body.Bytes(), &r)
	if r.Code != 0 {
		return fmt.Errorf("api errcode %d: %s", r.Code, r.Msg)
	}
	if r.FCode != 0 {
		return fmt.Errorf("api code %d: %s", r.FCode, r.FMsg)
	}
	return nil
}
