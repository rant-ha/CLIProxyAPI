package requestlog

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS request_logs (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	request_id        TEXT NOT NULL DEFAULT '',
	trace_id          TEXT NOT NULL DEFAULT '',
	ts                INTEGER NOT NULL,
	endpoint          TEXT NOT NULL DEFAULT '',
	provider          TEXT NOT NULL DEFAULT '',
	executor          TEXT NOT NULL DEFAULT '',
	model             TEXT NOT NULL DEFAULT '',
	model_alias       TEXT NOT NULL DEFAULT '',
	response_model    TEXT NOT NULL DEFAULT '',
	api_key           TEXT NOT NULL DEFAULT '',
	auth_id           TEXT NOT NULL DEFAULT '',
	auth_index        TEXT NOT NULL DEFAULT '',
	auth_type         TEXT NOT NULL DEFAULT '',
	session_id        TEXT NOT NULL DEFAULT '',
	source            TEXT NOT NULL DEFAULT '',
	stream            INTEGER NOT NULL DEFAULT 0,
	failed            INTEGER NOT NULL DEFAULT 0,
	status_code       INTEGER NOT NULL DEFAULT 0,
	latency_ms        INTEGER NOT NULL DEFAULT 0,
	ttft_ms           INTEGER NOT NULL DEFAULT 0,
	input_tokens      INTEGER NOT NULL DEFAULT 0,
	output_tokens     INTEGER NOT NULL DEFAULT 0,
	reasoning_tokens  INTEGER NOT NULL DEFAULT 0,
	cached_tokens     INTEGER NOT NULL DEFAULT 0,
	cache_read_tokens INTEGER NOT NULL DEFAULT 0,
	cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
	total_tokens      INTEGER NOT NULL DEFAULT 0,
	client_ip         TEXT NOT NULL DEFAULT '',
	user_agent        TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_request_logs_ts ON request_logs(ts);
CREATE INDEX IF NOT EXISTS idx_request_logs_trace ON request_logs(trace_id);
CREATE INDEX IF NOT EXISTS idx_request_logs_model ON request_logs(model);
CREATE INDEX IF NOT EXISTS idx_request_logs_response_model ON request_logs(response_model);
CREATE INDEX IF NOT EXISTS idx_request_logs_provider ON request_logs(provider);
CREATE INDEX IF NOT EXISTS idx_request_logs_api_key ON request_logs(api_key);
`

// Row is one persisted request log entry.
type Row struct {
	ID               int64  `json:"id"`
	RequestID        string `json:"request_id"`
	TraceID          string `json:"trace_id"`
	Timestamp        int64  `json:"ts"` // unix millis
	Endpoint         string `json:"endpoint"`
	Provider         string `json:"provider"`
	Executor         string `json:"executor"`
	Model            string `json:"model"`          // requested model
	ModelAlias       string `json:"model_alias"`    // requested alias
	ResponseModel    string `json:"response_model"` // real upstream model name
	APIKey           string `json:"api_key"`        // masked
	AuthID           string `json:"auth_id"`
	AuthIndex        string `json:"auth_index"`
	AuthType         string `json:"auth_type"`
	SessionID        string `json:"session_id"`
	Source           string `json:"source"`
	Stream           bool   `json:"stream"`
	Failed           bool   `json:"failed"`
	StatusCode       int    `json:"status_code"`
	LatencyMs        int64  `json:"latency_ms"`
	TTFTMs           int64  `json:"ttft_ms"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	ReasoningTokens  int64  `json:"reasoning_tokens"`
	CachedTokens     int64  `json:"cached_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	ClientIP         string `json:"client_ip"`
	UserAgent        string `json:"user_agent"`
}

// Filter narrows a log query. Zero values mean "no constraint".
type Filter struct {
	From          int64  // unix millis, inclusive
	To            int64  // unix millis, exclusive
	Model         string // requested model, exact match
	ResponseModel string // real upstream model, exact match
	Provider      string
	Executor      string
	APIKey        string
	Endpoint      string
	SessionID     string
	TraceID       string
	Failed        *bool
	Search        string // substring match on request_id/trace_id/session_id
	Page          int    // 1-based
	PageSize      int
}

func (f Filter) normalized() Filter {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 || f.PageSize > 500 {
		f.PageSize = 50
	}
	f.Model = strings.TrimSpace(f.Model)
	f.ResponseModel = strings.TrimSpace(f.ResponseModel)
	f.Provider = strings.TrimSpace(f.Provider)
	f.Executor = strings.TrimSpace(f.Executor)
	f.APIKey = strings.TrimSpace(f.APIKey)
	f.Endpoint = strings.TrimSpace(f.Endpoint)
	f.SessionID = strings.TrimSpace(f.SessionID)
	f.TraceID = strings.TrimSpace(f.TraceID)
	f.Search = strings.TrimSpace(f.Search)
	return f
}

// GroupCount is a grouped aggregation row for the stats endpoint.
type GroupCount struct {
	Key          string `json:"key"`
	Requests     int64  `json:"requests"`
	Failed       int64  `json:"failed"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	TotalTokens  int64  `json:"total_tokens"`
}

// DayBucket aggregates one calendar day (UTC).
type DayBucket struct {
	Day          string `json:"day"` // YYYY-MM-DD
	Requests     int64  `json:"requests"`
	Failed       int64  `json:"failed"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	TotalTokens  int64  `json:"total_tokens"`
}

// Stats is the dashboard aggregation payload.
type Stats struct {
	From              int64        `json:"from"`
	To                int64        `json:"to"`
	Requests          int64        `json:"requests"`
	Failed            int64        `json:"failed"`
	InputTokens       int64        `json:"input_tokens"`
	OutputTokens      int64        `json:"output_tokens"`
	TotalTokens       int64        `json:"total_tokens"`
	AvgLatencyMs      float64      `json:"avg_latency_ms"`
	DistinctModels    int64        `json:"distinct_models"`
	DistinctRealModels int64       `json:"distinct_real_models"`
	ByDay             []DayBucket  `json:"by_day"`
	ByModel           []GroupCount `json:"by_model"`
	ByRealModel       []GroupCount `json:"by_real_model"`
	ByProvider        []GroupCount `json:"by_provider"`
	ByAPIKey          []GroupCount `json:"by_api_key"`
}

// Store is a SQLite-backed request log store. It is safe for concurrent use.
type Store struct {
	mu   sync.Mutex
	db   *sql.DB
	path string
}

// Open creates (or opens) the SQLite database at path and ensures the schema.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?cache=shared")
	if err != nil {
		return nil, fmt.Errorf("requestlog: open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("requestlog: pragma wal: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("requestlog: schema: %w", err)
	}
	return &Store{db: db, path: path}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// Path returns the database file path.
func (s *Store) Path() string { return s.path }

const insertSQL = `INSERT INTO request_logs
(request_id, trace_id, ts, endpoint, provider, executor, model, model_alias, response_model,
 api_key, auth_id, auth_index, auth_type, session_id, source, stream, failed, status_code,
 latency_ms, ttft_ms, input_tokens, output_tokens, reasoning_tokens, cached_tokens,
 cache_read_tokens, cache_creation_tokens, total_tokens, client_ip, user_agent)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// InsertBatch inserts rows in a single transaction.
func (s *Store) InsertBatch(rows []Row) error {
	if len(rows) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, r := range rows {
		if _, err := stmt.Exec(
			r.RequestID, r.TraceID, r.Timestamp, r.Endpoint, r.Provider, r.Executor,
			r.Model, r.ModelAlias, r.ResponseModel, r.APIKey, r.AuthID, r.AuthIndex,
			r.AuthType, r.SessionID, r.Source, boolInt(r.Stream), boolInt(r.Failed),
			r.StatusCode, r.LatencyMs, r.TTFTMs, r.InputTokens, r.OutputTokens,
			r.ReasoningTokens, r.CachedTokens, r.CacheReadTokens, r.CacheCreationTokens,
			r.TotalTokens, r.ClientIP, r.UserAgent,
		); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func whereClause(f Filter, args *[]any) string {
	var conds []string
	add := func(cond string, v any) {
		conds = append(conds, cond)
		*args = append(*args, v)
	}
	if f.From > 0 {
		add("ts >= ?", f.From)
	}
	if f.To > 0 {
		add("ts < ?", f.To)
	}
	if f.Model != "" {
		add("model = ?", f.Model)
	}
	if f.ResponseModel != "" {
		add("response_model = ?", f.ResponseModel)
	}
	if f.Provider != "" {
		add("provider = ?", f.Provider)
	}
	if f.Executor != "" {
		add("executor = ?", f.Executor)
	}
	if f.APIKey != "" {
		add("api_key = ?", f.APIKey)
	}
	if f.Endpoint != "" {
		add("endpoint = ?", f.Endpoint)
	}
	if f.SessionID != "" {
		add("session_id = ?", f.SessionID)
	}
	if f.TraceID != "" {
		add("trace_id = ?", f.TraceID)
	}
	if f.Failed != nil {
		if *f.Failed {
			add("failed = ?", 1)
		} else {
			add("failed = ?", 0)
		}
	}
	if f.Search != "" {
		like := "%" + f.Search + "%"
		conds = append(conds, "(request_id LIKE ? OR trace_id LIKE ? OR session_id LIKE ?)")
		*args = append(*args, like, like, like)
	}
	if len(conds) == 0 {
		return ""
	}
	return "WHERE " + strings.Join(conds, " AND ")
}

const selectCols = `id, request_id, trace_id, ts, endpoint, provider, executor, model, model_alias,
	response_model, api_key, auth_id, auth_index, auth_type, session_id, source, stream, failed,
	status_code, latency_ms, ttft_ms, input_tokens, output_tokens, reasoning_tokens,
	cached_tokens, cache_read_tokens, cache_creation_tokens, total_tokens, client_ip, user_agent`

func scanRow(scanner interface {
	Scan(dest ...any) error
}) (Row, error) {
	var r Row
	var stream, failed int
	err := scanner.Scan(
		&r.ID, &r.RequestID, &r.TraceID, &r.Timestamp, &r.Endpoint, &r.Provider,
		&r.Executor, &r.Model, &r.ModelAlias, &r.ResponseModel, &r.APIKey, &r.AuthID,
		&r.AuthIndex, &r.AuthType, &r.SessionID, &r.Source, &stream, &failed,
		&r.StatusCode, &r.LatencyMs, &r.TTFTMs, &r.InputTokens, &r.OutputTokens,
		&r.ReasoningTokens, &r.CachedTokens, &r.CacheReadTokens, &r.CacheCreationTokens,
		&r.TotalTokens, &r.ClientIP, &r.UserAgent,
	)
	r.Stream = stream != 0
	r.Failed = failed != 0
	return r, err
}

// Query returns one page of rows plus the total matching count.
func (s *Store) Query(f Filter) ([]Row, int64, error) {
	f = f.normalized()
	s.mu.Lock()
	defer s.mu.Unlock()
	var args []any
	where := whereClause(f, &args)

	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM request_logs "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	offset := (f.Page - 1) * f.PageSize
	qargs := append(append([]any{}, args...), f.PageSize, offset)
	rows, err := s.db.Query(
		"SELECT "+selectCols+" FROM request_logs "+where+" ORDER BY ts DESC, id DESC LIMIT ? OFFSET ?",
		qargs...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if out == nil {
		out = []Row{}
	}
	return out, total, nil
}

// PurgeOlderThan deletes rows with ts < cutoffMillis and returns the count removed.
func (s *Store) PurgeOlderThan(cutoffMillis int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec("DELETE FROM request_logs WHERE ts < ?", cutoffMillis)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Stats aggregates logs in [from, to). Zero from/to means unbounded.
func (s *Store) Stats(from, to int64) (*Stats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var args []any
	where := ""
	if from > 0 || to > 0 {
		var conds []string
		if from > 0 {
			conds = append(conds, "ts >= ?")
			args = append(args, from)
		}
		if to > 0 {
			conds = append(conds, "ts < ?")
			args = append(args, to)
		}
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	st := &Stats{From: from, To: to, ByDay: []DayBucket{}, ByModel: []GroupCount{},
		ByRealModel: []GroupCount{}, ByProvider: []GroupCount{}, ByAPIKey: []GroupCount{}}

	err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(failed),0),
		COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(total_tokens),0),
		COALESCE(AVG(latency_ms),0), COUNT(DISTINCT model), COUNT(DISTINCT response_model)
		FROM request_logs `+where, args...).Scan(
		&st.Requests, &st.Failed, &st.InputTokens, &st.OutputTokens, &st.TotalTokens,
		&st.AvgLatencyMs, &st.DistinctModels, &st.DistinctRealModels)
	if err != nil {
		return nil, err
	}

	groupBy := func(col string, limit int) ([]GroupCount, error) {
		qargs := append(append([]any{}, args...), limit)
		rows, err := s.db.Query(`SELECT `+col+`, COUNT(*), COALESCE(SUM(failed),0),
			COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(total_tokens),0)
			FROM request_logs `+where+` GROUP BY `+col+` ORDER BY COUNT(*) DESC LIMIT ?`, qargs...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []GroupCount{}
		for rows.Next() {
			var g GroupCount
			if err := rows.Scan(&g.Key, &g.Requests, &g.Failed, &g.InputTokens, &g.OutputTokens, &g.TotalTokens); err != nil {
				return nil, err
			}
			if strings.TrimSpace(g.Key) == "" {
				g.Key = "(unknown)"
			}
			out = append(out, g)
		}
		return out, rows.Err()
	}
	if st.ByModel, err = groupBy("model", 20); err != nil {
		return nil, err
	}
	if st.ByRealModel, err = groupBy("response_model", 20); err != nil {
		return nil, err
	}
	if st.ByProvider, err = groupBy("provider", 20); err != nil {
		return nil, err
	}
	if st.ByAPIKey, err = groupBy("api_key", 20); err != nil {
		return nil, err
	}

	dayRows, err := s.db.Query(`SELECT strftime('%Y-%m-%d', ts/1000, 'unixepoch') AS day,
		COUNT(*), COALESCE(SUM(failed),0),
		COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(total_tokens),0)
		FROM request_logs `+where+` GROUP BY day ORDER BY day`, args...)
	if err != nil {
		return nil, err
	}
	defer dayRows.Close()
	for dayRows.Next() {
		var d DayBucket
		if err := dayRows.Scan(&d.Day, &d.Requests, &d.Failed, &d.InputTokens, &d.OutputTokens, &d.TotalTokens); err != nil {
			return nil, err
		}
		st.ByDay = append(st.ByDay, d)
	}
	if err := dayRows.Err(); err != nil {
		return nil, err
	}
	return st, nil
}
