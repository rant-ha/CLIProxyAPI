package requestlog

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

func TestPluginEndToEnd(t *testing.T) {
	dir := t.TempDir()
	SetDBPath(filepath.Join(dir, "e2e.db"))
	SetRetentionDays(30)
	SetEnabled(true)
	t.Cleanup(func() {
		SetEnabled(false)
		SetDBPath("")
		SetRetentionDays(DefaultRetentionDays)
	})

	p := &requestLogPlugin{}
	now := time.Now()
	p.HandleUsage(context.Background(), coreusage.Record{
		RequestID:     "exec-uuid-1",
		TraceID:       "trace-abc",
		Provider:      "claude",
		ExecutorType:  "ClaudeExecutor",
		Model:         "claude-sonnet-4-5",
		Alias:         "claude-sonnet-4-5",
		ResponseModel: "claude-sonnet-4-5-20250929",
		APIKey:        "sk-test-abcdef1234567890",
		AuthID:        "auth-1",
		AuthType:      "oauth",
		RequestedAt:   now,
		Latency:       1500 * time.Millisecond,
		Detail: coreusage.Detail{
			InputTokens:  100,
			OutputTokens: 50,
			TotalTokens:  150,
		},
	})

	deadline := time.Now().Add(10 * time.Second)
	for {
		st, err := QueryStore()
		if err != nil {
			t.Fatalf("querystore: %v", err)
		}
		rows, total, err := st.Query(Filter{})
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if total == 1 {
			r := rows[0]
			if r.Model != "claude-sonnet-4-5" {
				t.Fatalf("model: %q", r.Model)
			}
			if r.ResponseModel != "claude-sonnet-4-5-20250929" {
				t.Fatalf("response_model: %q", r.ResponseModel)
			}
			if r.APIKey != "sk-t...7890" {
				t.Fatalf("api_key mask: %q", r.APIKey)
			}
			if r.TotalTokens != 150 || r.LatencyMs != 1500 {
				t.Fatalf("tokens/latency: %+v", r)
			}
			if r.TraceID != "trace-abc" || r.RequestID != "exec-uuid-1" {
				t.Fatalf("ids: %+v", r)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for flush, total=%d", total)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// stats should see it too
	st, _ := QueryStore()
	stats, err := st.Stats(0, 0)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Requests != 1 || len(stats.ByRealModel) != 1 || stats.ByRealModel[0].Key != "claude-sonnet-4-5-20250929" {
		t.Fatalf("stats: %+v", stats)
	}
}
