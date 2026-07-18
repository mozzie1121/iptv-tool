package logger

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/natefinch/lumberjack.v2"
)

// LogAppender is the interface for appending formatted log lines to an external buffer.
// This decouples the logger package from the api package to avoid import cycles.
type LogAppender interface {
	Append(level, content string)
}

// bufferHandler wraps a slog.Handler and also writes formatted log lines
// to an external LogAppender (e.g. a ring buffer for the web UI).
type bufferHandler struct {
	inner    slog.Handler
	appender LogAppender
	mu       sync.Mutex
	buf      bytes.Buffer
}

// newBufferHandler creates a handler that tees log output to both the
// inner handler and the appender.
func newBufferHandler(inner slog.Handler, appender LogAppender) *bufferHandler {
	return &bufferHandler{
		inner:    inner,
		appender: appender,
	}
}

func (h *bufferHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *bufferHandler) Handle(ctx context.Context, r slog.Record) error {
	// Write to the main handler (console + file)
	err := h.inner.Handle(ctx, r)

	// Also format and append to the ring buffer
	// Use a temporary TextHandler writing to a buffer to get the formatted line
	h.mu.Lock()
	h.buf.Reset()
	tmp := slog.NewTextHandler(&h.buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	_ = tmp.Handle(ctx, r)
	line := h.buf.String()
	h.mu.Unlock()

	// Trim trailing newline
	if len(line) > 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
	}

	// Map slog level to a short string
	level := "INFO"
	switch {
	case r.Level >= slog.LevelError:
		level = "ERROR"
	case r.Level >= slog.LevelWarn:
		level = "WARN"
	case r.Level >= slog.LevelInfo:
		level = "INFO"
	default:
		level = "DEBUG"
	}

	h.appender.Append(level, line)

	return err
}

func (h *bufferHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return newBufferHandler(h.inner.WithAttrs(attrs), h.appender)
}

func (h *bufferHandler) WithGroup(name string) slog.Handler {
	return newBufferHandler(h.inner.WithGroup(name), h.appender)
}

// InitLogger initializes the global slog logger with rotation and console output.
// If appender is non-nil, log records are also teed into it for the web UI.
func InitLogger(logDir string, appender LogAppender) error {
	// Ensure log directory exists
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}

	logFile := filepath.Join(logDir, "iptv-server.log")

	// Configure lumberjack for log rotation
	// MaxSize: 10 MB, MaxBackups: 5, MaxAge: 30 days
	rotator := &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    10, // megabytes
		MaxBackups: 5,
		MaxAge:     30, // days
		Compress:   true,
	}

	// Create a multi-writer to write to both console and file
	multiWriter := io.MultiWriter(os.Stdout, rotator)

	// Configure slog handler
	handlerOptions := &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}

	// Using TextHandler for readable logs
	textHandler := slog.NewTextHandler(multiWriter, handlerOptions)

	var handler slog.Handler = textHandler
	if appender != nil {
		handler = newBufferHandler(textHandler, appender)
	}

	// Create logger and set it as the default
	logger := slog.New(handler)
	slog.SetDefault(logger)

	return nil
}

// Fatalf is a helper function to log an error message and exit the program.
// slog doesn't have a built-in Fatal method.
func Fatalf(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}
