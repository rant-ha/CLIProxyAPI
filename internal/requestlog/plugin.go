package requestlog

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	internallogging "github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

const (
	writeChanSize  = 4096
	writeBatchSize = 200
	writeFlushTick = time.Second
	purgeTick      = time.Hour
)

func init() {
	coreusage.RegisterPlugin(&requestLogPlugin{})
	globalWriter.start()
}

// maskAPIKey hides a client API key while keeping it identifiable for grouping.
// Only a short prefix/suffix is kept; short values are fully masked.
func maskAPIKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if len(key) <= 12 {
		return "***"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

type requestLogPlugin struct{}

func (p *requestLogPlugin) HandleUsage(ctx context.Context, record coreusage.Record) {
	if p == nil || !Enabled() {
		return
	}
	globalWriter.enqueue(rowFromRecord(ctx, record))
}

func rowFromRecord(ctx context.Context, record coreusage.Record) Row {
	ts := record.RequestedAt
	if ts.IsZero() {
		ts = time.Now()
	}
	model := strings.TrimSpace(record.Model)
	if model == "" {
		model = "unknown"
	}
	alias := strings.TrimSpace(record.Alias)
	if alias == "" {
		alias = model
	}
	provider := strings.TrimSpace(record.Provider)
	if provider == "" {
		provider = "unknown"
	}
	executor := strings.TrimSpace(record.ExecutorType)
	if executor == "" {
		executor = "unknown"
	}
	authType := strings.TrimSpace(record.AuthType)
	if authType == "" {
		authType = "unknown"
	}
	clientMeta := internallogging.GetClientRequestMetadata(ctx)
	sessionID := strings.TrimSpace(record.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(clientRequestMetadataSession(ctx, clientMeta))
	}
	detail := coreusage.EnsureTokenBreakdownForProvider(record.Detail, record.Provider, record.ExecutorType)

	failed := record.Failed
	statusCode := record.Fail.StatusCode
	if !failed {
		if s := internallogging.GetResponseStatus(ctx); s >= 400 {
			failed = true
		}
		if statusCode <= 0 {
			statusCode = 200
		}
	} else if statusCode <= 0 {
		if s := internallogging.GetResponseStatus(ctx); s > 0 {
			statusCode = s
		} else {
			statusCode = 500
		}
	}
	stream := record.Stream
	if !stream {
		stream = coreusage.StreamFromContext(ctx)
	}
	traceID := strings.TrimSpace(record.TraceID)
	if traceID == "" {
		traceID = strings.TrimSpace(internallogging.GetRequestID(ctx))
	}
	requestID := strings.TrimSpace(record.RequestID)

	return Row{
		RequestID:         requestID,
		TraceID:           traceID,
		Timestamp:         ts.UnixMilli(),
		Endpoint:          strings.TrimSpace(internallogging.GetEndpoint(ctx)),
		Provider:          provider,
		Executor:          executor,
		Model:             model,
		ModelAlias:        alias,
		ResponseModel:     strings.TrimSpace(record.ResponseModel),
		APIKey:            maskAPIKey(record.APIKey),
		AuthID:            strings.TrimSpace(record.AuthID),
		AuthIndex:         strings.TrimSpace(record.AuthIndex),
		AuthType:          authType,
		SessionID:         sessionID,
		Source:            strings.TrimSpace(record.Source),
		Stream:            stream,
		Failed:            failed,
		StatusCode:        statusCode,
		LatencyMs:         record.Latency.Milliseconds(),
		TTFTMs:            record.TTFT.Milliseconds(),
		InputTokens:       detail.InputTokens,
		OutputTokens:      detail.OutputTokens,
		ReasoningTokens:   detail.ReasoningTokens,
		CachedTokens:      detail.CachedTokens,
		CacheReadTokens:   detail.CacheReadTokens,
		CacheCreationTokens: detail.CacheCreationTokens,
		TotalTokens:       detail.TotalTokens,
		ClientIP:          clientMeta.ClientIP,
		UserAgent:         clientMeta.UserAgent,
	}
}

// clientRequestMetadataSession mirrors the redisqueue fallback for session ids.
func clientRequestMetadataSession(ctx context.Context, meta internallogging.ClientRequestMetadata) string {
	_ = ctx
	return meta.SessionID
}

// writer owns the SQLite store and drains the async channel in batches.
type writer struct {
	ch     chan Row
	mu     sync.Mutex
	store  *Store
	path   string
	stopCh chan struct{}
	once   sync.Once
}

var globalWriter = &writer{ch: make(chan Row, writeChanSize), stopCh: make(chan struct{})}

func (w *writer) start() {
	w.once.Do(func() {
		go w.loop()
	})
}

func (w *writer) enqueue(r Row) {
	select {
	case w.ch <- r:
	default:
		// Never block request handling; drop under extreme pressure.
	}
}

func (w *writer) fallbackDir() string {
	if p := util.WritablePath(); p != "" {
		return p
	}
	return "."
}

// ensureStoreLocked lazily opens the database on first write and reopens it
// when the configured path changes (e.g. after a config reload).
// The caller must hold w.mu.
func (w *writer) ensureStoreLocked() (*Store, error) {
	path := ResolveDBPath(w.fallbackDir())
	if w.store != nil {
		if w.store.Path() == path {
			return w.store, nil
		}
		_ = w.store.Close()
		w.store = nil
	}
	st, err := Open(path)
	if err != nil {
		return nil, err
	}
	w.store = st
	w.path = path
	w.purge()
	return st, nil
}

func (w *writer) ensureStore() (*Store, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ensureStoreLocked()
}

// purge deletes rows older than the retention window. retentionDays == 0 keeps everything.
func (w *writer) purge() {
	days := RetentionDays()
	if days <= 0 || w.store == nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -days).UnixMilli()
	n, err := w.store.PurgeOlderThan(cutoff)
	if err != nil {
		log.Printf("requestlog: purge failed: %v", err)
		return
	}
	if n > 0 {
		log.Printf("requestlog: purged %d rows older than %d days", n, days)
	}
}

func (w *writer) loop() {
	ticker := time.NewTicker(writeFlushTick)
	defer ticker.Stop()
	purgeTicker := time.NewTicker(purgeTick)
	defer purgeTicker.Stop()
	var batch []Row
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if !Enabled() {
			batch = batch[:0]
			return
		}
		st, err := w.ensureStore()
		if err != nil {
			log.Printf("requestlog: open store failed: %v", err)
			batch = batch[:0]
			return
		}
		if err := st.InsertBatch(batch); err != nil {
			log.Printf("requestlog: insert batch failed: %v", err)
		}
		batch = batch[:0]
	}
	for {
		select {
		case <-w.stopCh:
			flush()
			return
		case r := <-w.ch:
			batch = append(batch, r)
			if len(batch) >= writeBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-purgeTicker.C:
			w.mu.Lock()
			w.purge()
			w.mu.Unlock()
		}
	}
}

// QueryStore opens (if needed) and returns the store for management API reads.
func QueryStore() (*Store, error) {
	return globalWriter.ensureStore()
}

// PurgeNow immediately deletes rows older than the retention window.
func PurgeNow() (int64, error) {
	globalWriter.mu.Lock()
	defer globalWriter.mu.Unlock()
	st, err := globalWriter.ensureStoreLocked()
	if err != nil {
		return 0, err
	}
	days := RetentionDays()
	if days <= 0 {
		return 0, nil
	}
	return st.PurgeOlderThan(time.Now().AddDate(0, 0, -days).UnixMilli())
}
