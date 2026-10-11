// Package requestlog persists per-request usage records (including the real
// upstream model name reported by the provider response) into a local SQLite
// database with configurable retention, and serves them through the
// management API and a built-in dashboard page.
//
// Records arrive through the sdk/cliproxy/usage plugin bus, so no executor,
// translator or handler code needs to change: the usage.Record already
// carries the requested model, the upstream ResponseModel, token usage,
// latency, auth identity and client metadata.
package requestlog

import (
	"path/filepath"
	"strings"
	"sync/atomic"
)

const (
	// DefaultRetentionDays is used when retention-days is not configured.
	DefaultRetentionDays = 30
	// DefaultDBFileName is used when db-path is not configured.
	DefaultDBFileName = "requestlog.db"
)

var (
	enabled       atomic.Bool
	dbPath        atomic.Value // string
	retentionDays atomic.Int64
)

func init() {
	retentionDays.Store(DefaultRetentionDays)
	dbPath.Store("")
}

// SetEnabled toggles whether usage records are persisted.
func SetEnabled(v bool) { enabled.Store(v) }

// Enabled reports whether request logging is active.
func Enabled() bool { return enabled.Load() }

// SetDBPath overrides the SQLite file path. Empty means "use the default".
func SetDBPath(p string) { dbPath.Store(strings.TrimSpace(p)) }

// DBPath returns the configured SQLite path, or "" for the default.
func DBPath() string {
	if v, ok := dbPath.Load().(string); ok {
		return v
	}
	return ""
}

// SetRetentionDays sets how many days of logs are kept. Values < 0 are
// treated as 0, which disables expiration (keep forever).
func SetRetentionDays(days int) {
	if days < 0 {
		days = 0
	}
	retentionDays.Store(int64(days))
}

// RetentionDays returns the configured retention in days (0 = keep forever).
func RetentionDays() int { return int(retentionDays.Load()) }

// ResolveDBPath returns the effective SQLite path: the configured db-path,
// or fallbackDir/DefaultDBFileName when unset.
func ResolveDBPath(fallbackDir string) string {
	if p := DBPath(); p != "" {
		return p
	}
	dir := strings.TrimSpace(fallbackDir)
	if dir == "" {
		dir = "."
	}
	return filepath.Join(dir, DefaultDBFileName)
}
