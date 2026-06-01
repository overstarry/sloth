package llm

import "context"

// mockProvider returns a deterministic placeholder diagnosis. It lets the full
// pipeline run end-to-end without an API key during development and tests.
type mockProvider struct{}

func (m *mockProvider) Name() string { return "mock" }

func (m *mockProvider) Complete(ctx context.Context, req Request) (string, error) {
	return `{
  "root_cause": "Sequential scan on a large table due to a missing index on the filter column (mock diagnosis).",
  "risk_level": "warn",
  "suggestions": [
    {
      "kind": "index",
      "title": "Add a btree index on the filter column",
      "ddl": "CREATE INDEX CONCURRENTLY idx_example_col ON example (col);",
      "detail": "The planner falls back to a seq scan; a btree index makes the predicate sargable.",
      "expected_gain": "Large tables: O(n) -> O(log n) lookups."
    }
  ]
}`, nil
}
