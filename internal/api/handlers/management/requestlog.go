package management

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/requestlog"
)

// RequestLogDashboard serves the built-in request log dashboard page.
// It is intentionally outside the management-key middleware: the page itself
// prompts for the management key (kept in sessionStorage) before calling the
// data APIs below, which remain protected.
func (h *Handler) RequestLogDashboard(c *gin.Context) {
	if h == nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	_, _ = c.Writer.Write(requestlog.DashboardHTML())
}

func requestLogStore(c *gin.Context) (*requestlog.Store, bool) {
	if !requestlog.Enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "request log is disabled (observability.request-log.enabled)"})
		return nil, false
	}
	st, err := requestlog.QueryStore()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nil, false
	}
	return st, true
}

func queryInt(c *gin.Context, name string) int64 {
	v := strings.TrimSpace(c.Query(name))
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// ListRequestLogs returns one page of persisted request logs with filters.
func (h *Handler) ListRequestLogs(c *gin.Context) {
	if h == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "handler not initialized"})
		return
	}
	st, ok := requestLogStore(c)
	if !ok {
		return
	}
	var failed *bool
	if fv := strings.TrimSpace(c.Query("failed")); fv != "" {
		b := fv == "1" || strings.EqualFold(fv, "true")
		failed = &b
	}
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.Query("page_size"))
	if pageSize < 1 || pageSize > 500 {
		pageSize = 50
	}
	rows, total, err := st.Query(requestlog.Filter{
		From:          queryInt(c, "from"),
		To:            queryInt(c, "to"),
		Model:         c.Query("model"),
		ResponseModel: c.Query("response_model"),
		Provider:      c.Query("provider"),
		Executor:      c.Query("executor"),
		APIKey:        c.Query("api_key"),
		Endpoint:      c.Query("endpoint"),
		SessionID:     c.Query("session_id"),
		TraceID:       c.Query("trace_id"),
		Failed:        failed,
		Search:        c.Query("search"),
		Page:          page,
		PageSize:      pageSize,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"rows": rows, "total": total, "page": page, "page_size": pageSize})
}

// RequestLogStats returns dashboard aggregations for a time range.
func (h *Handler) RequestLogStats(c *gin.Context) {
	if h == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "handler not initialized"})
		return
	}
	st, ok := requestLogStore(c)
	if !ok {
		return
	}
	stats, err := st.Stats(queryInt(c, "from"), queryInt(c, "to"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, stats)
}

// requestLogConfigPayload is the runtime-tunable subset of the request log config.
type requestLogConfigPayload struct {
	Enabled       *bool   `json:"enabled"`
	DBPath        *string `json:"db_path"`
	RetentionDays *int    `json:"retention_days"`
}

// GetRequestLogConfig returns the current request log configuration and status.
func (h *Handler) GetRequestLogConfig(c *gin.Context) {
	if h == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "handler not initialized"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"enabled":        requestlog.Enabled(),
		"db_path":        requestlog.ResolveDBPath(""),
		"retention_days": requestlog.RetentionDays(),
	})
}

// PutRequestLogConfig updates the request log configuration at runtime.
// Note: to persist across restarts, also set observability.request-log in config.yaml.
func (h *Handler) PutRequestLogConfig(c *gin.Context) {
	if h == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "handler not initialized"})
		return
	}
	var p requestLogConfigPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if p.Enabled != nil {
		requestlog.SetEnabled(*p.Enabled)
	}
	if p.DBPath != nil {
		requestlog.SetDBPath(*p.DBPath)
	}
	if p.RetentionDays != nil {
		requestlog.SetRetentionDays(*p.RetentionDays)
	}
	c.JSON(http.StatusOK, gin.H{
		"enabled":        requestlog.Enabled(),
		"db_path":        requestlog.ResolveDBPath(""),
		"retention_days": requestlog.RetentionDays(),
	})
}

// PurgeRequestLogs immediately deletes logs older than the retention window.
func (h *Handler) PurgeRequestLogs(c *gin.Context) {
	if h == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "handler not initialized"})
		return
	}
	if !requestlog.Enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "request log is disabled"})
		return
	}
	n, err := requestlog.PurgeNow()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"purged": n, "retention_days": requestlog.RetentionDays()})
}
