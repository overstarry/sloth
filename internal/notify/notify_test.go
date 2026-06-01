package notify

import (
	"testing"
	"time"

	"github.com/overstarry/sloth/internal/model"
)

func TestTokenBucket_RefillsPerInterval(t *testing.T) {
	base := time.Unix(0, 0)
	cur := base
	nowFunc = func() time.Time { return cur }
	defer func() { nowFunc = time.Now }()

	b := newTokenBucket(2, time.Minute)
	if !b.tryTake() || !b.tryTake() {
		t.Fatal("first two takes should succeed")
	}
	if b.tryTake() {
		t.Fatal("third take should fail before refill")
	}
	cur = base.Add(time.Minute) // advance past interval
	if !b.tryTake() {
		t.Fatal("take after refill should succeed")
	}
}

func TestRenderMarkdown_IncludesTitleAndLink(t *testing.T) {
	out := renderMarkdown(Message{
		Title:   "慢 SQL",
		Level:   model.LevelCritical,
		Summary: "abc 200ms",
		Detail:  "root cause",
		Link:    "http://x/y",
	})
	for _, want := range []string{"慢 SQL", "abc 200ms", "root cause", "http://x/y", "🔴"} {
		if !contains(out, want) {
			t.Errorf("markdown missing %q in:\n%s", want, out)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
