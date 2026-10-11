package requestlog

import (
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func sampleRow(ts int64, model, responseModel, provider string, failed bool) Row {
	return Row{
		RequestID:     "req-1",
		TraceID:       "trace-1",
		Timestamp:     ts,
		Endpoint:      "/v1/messages",
		Provider:      provider,
		Executor:      "ClaudeExecutor",
		Model:         model,
		ModelAlias:    model,
		ResponseModel: responseModel,
		APIKey:        "sk-a...9123",
		Stream:        true,
		Failed:        failed,
		StatusCode:    200,
		LatencyMs:     1200,
		InputTokens:   100,
		OutputTokens:  50,
		TotalTokens:   150,
	}
}

func TestStoreInsertQuery(t *testing.T) {
	st := openTestStore(t)
	now := time.Now().UnixMilli()
	rows := []Row{
		sampleRow(now-1000, "claude-sonnet-4-5", "claude-sonnet-4-5-20250929", "claude", false),
		sampleRow(now-2000, "gpt-5", "gpt-5-2026-01-01", "openai", false),
		sampleRow(now-3000, "claude-sonnet-4-5", "claude-sonnet-4-5-20250929", "claude", true),
	}
	if err := st.InsertBatch(rows); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, total, err := st.Query(Filter{})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if total != 3 || len(got) != 3 {
		t.Fatalf("want 3 rows, got total=%d len=%d", total, len(got))
	}
	// newest first
	if got[0].Timestamp != now-1000 {
		t.Fatalf("ordering wrong: %d", got[0].Timestamp)
	}

	// filter by response model
	got, total, err = st.Query(Filter{ResponseModel: "claude-sonnet-4-5-20250929"})
	if err != nil || total != 2 {
		t.Fatalf("response_model filter: total=%d err=%v", total, err)
	}

	// failed filter
	f := true
	got, total, err = st.Query(Filter{Failed: &f})
	if err != nil || total != 1 {
		t.Fatalf("failed filter: total=%d err=%v", total, err)
	}
	_ = got

	// pagination
	got, total, err = st.Query(Filter{Page: 2, PageSize: 2})
	if err != nil || total != 3 || len(got) != 1 {
		t.Fatalf("pagination: total=%d len=%d err=%v", total, len(got), err)
	}

	// search
	got, total, err = st.Query(Filter{Search: "trace-1"})
	if err != nil || total != 3 {
		t.Fatalf("search: total=%d err=%v", total, err)
	}
}

func TestStoreStats(t *testing.T) {
	st := openTestStore(t)
	now := time.Now().UnixMilli()
	day := now - now%86400000
	rows := []Row{
		sampleRow(day+1000, "m1", "real-m1", "p1", false),
		sampleRow(day+2000, "m1", "real-m2", "p1", false),
		sampleRow(day+3000, "m2", "real-m2", "p2", true),
	}
	if err := st.InsertBatch(rows); err != nil {
		t.Fatalf("insert: %v", err)
	}
	stats, err := st.Stats(0, 0)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Requests != 3 || stats.Failed != 1 {
		t.Fatalf("counts: %+v", stats)
	}
	if stats.TotalTokens != 450 {
		t.Fatalf("tokens: %d", stats.TotalTokens)
	}
	if stats.DistinctRealModels != 2 {
		t.Fatalf("distinct real models: %d", stats.DistinctRealModels)
	}
	if len(stats.ByDay) != 1 || stats.ByDay[0].Requests != 3 {
		t.Fatalf("by_day: %+v", stats.ByDay)
	}
	if len(stats.ByRealModel) != 2 {
		t.Fatalf("by_real_model: %+v", stats.ByRealModel)
	}
	if stats.ByRealModel[0].Key != "real-m2" || stats.ByRealModel[0].Requests != 2 {
		t.Fatalf("by_real_model order: %+v", stats.ByRealModel)
	}
}

func TestStorePurge(t *testing.T) {
	st := openTestStore(t)
	now := time.Now().UnixMilli()
	old := now - 40*86400000
	rows := []Row{
		sampleRow(old, "m1", "r1", "p1", false),
		sampleRow(now, "m1", "r1", "p1", false),
	}
	if err := st.InsertBatch(rows); err != nil {
		t.Fatalf("insert: %v", err)
	}
	n, err := st.PurgeOlderThan(now - 30*86400000)
	if err != nil || n != 1 {
		t.Fatalf("purge: n=%d err=%v", n, err)
	}
	_, total, err := st.Query(Filter{})
	if err != nil || total != 1 {
		t.Fatalf("after purge: total=%d err=%v", total, err)
	}
}

func TestMaskAPIKey(t *testing.T) {
	if got := maskAPIKey("sk-ant-abcdef1234567890"); got != "sk-a...7890" {
		t.Fatalf("mask: %q", got)
	}
	if got := maskAPIKey("short"); got != "***" {
		t.Fatalf("mask short: %q", got)
	}
	if got := maskAPIKey(""); got != "" {
		t.Fatalf("mask empty: %q", got)
	}
}

func TestConfigDefaults(t *testing.T) {
	if RetentionDays() != DefaultRetentionDays {
		t.Fatalf("default retention: %d", RetentionDays())
	}
	if got := ResolveDBPath("/data"); got != "/data/requestlog.db" {
		t.Fatalf("resolve: %q", got)
	}
	SetDBPath("/tmp/x.db")
	if got := ResolveDBPath("/data"); got != "/tmp/x.db" {
		t.Fatalf("resolve override: %q", got)
	}
	SetDBPath("")
	SetRetentionDays(-5)
	if RetentionDays() != 0 {
		t.Fatalf("negative retention: %d", RetentionDays())
	}
	SetRetentionDays(DefaultRetentionDays)
}
