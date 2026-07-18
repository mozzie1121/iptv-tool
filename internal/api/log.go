package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// ---- Runtime Log Ring Buffer ----

// RuntimeLogEntry represents a single runtime log line
type RuntimeLogEntry struct {
	ID      uint64 `json:"id"`
	Time    string `json:"time"`
	Level   string `json:"level"`
	Content string `json:"content"`
}

// RuntimeLogBuffer is a thread-safe, fixed-size ring buffer for runtime logs
type RuntimeLogBuffer struct {
	mu      sync.Mutex
	entries []RuntimeLogEntry
	head    int    // write position (next slot to overwrite)
	count   int    // current number of entries (≤ cap)
	nextID  uint64 // monotonically increasing ID
	cap     int
}

// NewRuntimeLogBuffer creates a ring buffer with the given capacity
func NewRuntimeLogBuffer(capacity int) *RuntimeLogBuffer {
	return &RuntimeLogBuffer{
		entries: make([]RuntimeLogEntry, capacity),
		cap:     capacity,
	}
}

// Append adds a log line to the buffer
func (b *RuntimeLogBuffer) Append(level, content string) {
	b.mu.Lock()
	b.nextID++
	b.entries[b.head] = RuntimeLogEntry{
		ID:      b.nextID,
		Time:    time.Now().Format("2006-01-02 15:04:05"),
		Level:   level,
		Content: content,
	}
	b.head = (b.head + 1) % b.cap
	if b.count < b.cap {
		b.count++
	}
	b.mu.Unlock()
}

// Since returns entries with ID > sinceID, newest first, up to limit (0 = no limit)
func (b *RuntimeLogBuffer) Since(sinceID uint64, limit int) []RuntimeLogEntry {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.count == 0 {
		return nil
	}

	// Iterate from newest to oldest
	result := make([]RuntimeLogEntry, 0, min(b.count, 200))
	for i := b.count - 1; i >= 0; i-- {
		idx := (b.head - b.count + i + b.cap) % b.cap
		entry := b.entries[idx]
		if entry.ID > sinceID {
			result = append(result, entry)
			if limit > 0 && len(result) >= limit {
				break
			}
		}
	}
	return result
}

// All returns all entries in order (newest first)
func (b *RuntimeLogBuffer) All() []RuntimeLogEntry {
	return b.Since(0, 0)
}

// Clear empties the buffer
func (b *RuntimeLogBuffer) Clear() {
	b.mu.Lock()
	b.head = 0
	b.count = 0
	// keep nextID so clients don't re-fetch stale data
	b.mu.Unlock()
}

// ---- Access Log Ring Buffer ----

// AccessLogEntry represents a single access log record
type AccessLogEntry struct {
	ID        uint64 `json:"id"`
	Time      string `json:"time"`
	ClientIP  string `json:"client_ip"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Status    int    `json:"status"`
	Latency   string `json:"latency"`
	UserAgent string `json:"user_agent"`
}

// AccessLogBuffer is a thread-safe, fixed-size ring buffer for access logs
type AccessLogBuffer struct {
	mu      sync.Mutex
	entries []AccessLogEntry
	head    int
	count   int
	nextID  uint64
	cap     int
}

// NewAccessLogBuffer creates a ring buffer with the given capacity
func NewAccessLogBuffer(capacity int) *AccessLogBuffer {
	return &AccessLogBuffer{
		entries: make([]AccessLogEntry, capacity),
		cap:     capacity,
	}
}

// Append adds an access log entry
func (b *AccessLogBuffer) Append(entry AccessLogEntry) {
	b.mu.Lock()
	b.nextID++
	entry.ID = b.nextID
	b.entries[b.head] = entry
	b.head = (b.head + 1) % b.cap
	if b.count < b.cap {
		b.count++
	}
	b.mu.Unlock()
}

// Since returns entries with ID > sinceID, newest first, up to limit (0 = no limit)
func (b *AccessLogBuffer) Since(sinceID uint64, limit int) []AccessLogEntry {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.count == 0 {
		return nil
	}

	// Iterate from newest to oldest
	result := make([]AccessLogEntry, 0, min(b.count, 200))
	for i := b.count - 1; i >= 0; i-- {
		idx := (b.head - b.count + i + b.cap) % b.cap
		entry := b.entries[idx]
		if entry.ID > sinceID {
			result = append(result, entry)
			if limit > 0 && len(result) >= limit {
				break
			}
		}
	}
	return result
}

// All returns all entries in order (newest first)
func (b *AccessLogBuffer) All() []AccessLogEntry {
	return b.Since(0, 0)
}

// Clear empties the buffer
func (b *AccessLogBuffer) Clear() {
	b.mu.Lock()
	b.head = 0
	b.count = 0
	b.mu.Unlock()
}

// ---- Log API Controller ----

// LogController handles log-related API requests
type LogController struct {
	runtimeBuf *RuntimeLogBuffer
	accessBuf  *AccessLogBuffer
}

// NewLogController creates a new log controller
func NewLogController(runtimeBuf *RuntimeLogBuffer, accessBuf *AccessLogBuffer) *LogController {
	return &LogController{
		runtimeBuf: runtimeBuf,
		accessBuf:  accessBuf,
	}
}

// GetRuntimeLogs returns runtime log entries since a given ID
// GET /api/logs/runtime?since=0&limit=500
func (lc *LogController) GetRuntimeLogs(c *gin.Context) {
	sinceID, _ := strconv.ParseUint(c.DefaultQuery("since", "0"), 10, 64)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "0"))

	entries := lc.runtimeBuf.Since(sinceID, limit)
	if entries == nil {
		entries = []RuntimeLogEntry{}
	}
	c.JSON(http.StatusOK, gin.H{"entries": entries})
}

// GetAccessLogs returns access log entries since a given ID
// GET /api/logs/access?since=0&limit=500
func (lc *LogController) GetAccessLogs(c *gin.Context) {
	sinceID, _ := strconv.ParseUint(c.DefaultQuery("since", "0"), 10, 64)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "0"))

	entries := lc.accessBuf.Since(sinceID, limit)
	if entries == nil {
		entries = []AccessLogEntry{}
	}
	c.JSON(http.StatusOK, gin.H{"entries": entries})
}

// ClearRuntimeLogs clears all runtime logs
// DELETE /api/logs/runtime
func (lc *LogController) ClearRuntimeLogs(c *gin.Context) {
	lc.runtimeBuf.Clear()
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

// ClearAccessLogs clears all access logs
// DELETE /api/logs/access
func (lc *LogController) ClearAccessLogs(c *gin.Context) {
	lc.accessBuf.Clear()
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

// DownloadRuntimeLogs downloads runtime logs as a text file
// GET /api/logs/runtime/download
func (lc *LogController) DownloadRuntimeLogs(c *gin.Context) {
	entries := lc.runtimeBuf.All()

	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(e.Time)
		sb.WriteString(" ")
		sb.WriteString(e.Content)
		sb.WriteString("\n")
	}

	filename := fmt.Sprintf("runtime_%s.log", time.Now().Format("20060102_150405"))
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(sb.String()))
}

// DownloadAccessLogs downloads access logs as a text file
// GET /api/logs/access/download
func (lc *LogController) DownloadAccessLogs(c *gin.Context) {
	entries := lc.accessBuf.All()

	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("%s | %s | %s %s | %d | %s | %s\n",
			e.Time, e.ClientIP, e.Method, e.Path, e.Status, e.Latency, e.UserAgent))
	}

	filename := fmt.Sprintf("access_%s.log", time.Now().Format("20060102_150405"))
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(sb.String()))
}

// RegisterLogRoutes registers all log API routes under an authorized group.
// It also returns a middleware function for access logging.
func RegisterLogRoutes(authorized *gin.RouterGroup, runtimeBuf *RuntimeLogBuffer, accessBuf *AccessLogBuffer) {
	logCtrl := NewLogController(runtimeBuf, accessBuf)

	authorized.GET("/logs/runtime", logCtrl.GetRuntimeLogs)
	authorized.GET("/logs/access", logCtrl.GetAccessLogs)
	authorized.DELETE("/logs/runtime", logCtrl.ClearRuntimeLogs)
	authorized.DELETE("/logs/access", logCtrl.ClearAccessLogs)

	// Download endpoints — these need auth but use a special path pattern
	// We register them under authorized group but handle Content-Disposition ourselves
	authorized.GET("/logs/runtime/download", logCtrl.DownloadRuntimeLogs)
	authorized.GET("/logs/access/download", logCtrl.DownloadAccessLogs)
}
