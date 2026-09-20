// Package logging configures process-wide structured logging via log/slog.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Config selects the slog handler used by the process.
type Config struct {
	// Level is debug, info, warn, or error. Empty means info.
	Level string
	// Format is text or json. Empty means text.
	Format string
	// Out is the log destination. Nil means os.Stderr.
	Out io.Writer
}

// ParseLevel maps a user-facing level name to slog.Level.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level %q (want debug, info, warn, error)", s)
	}
}

// New builds a logger without installing it as the process default.
func New(cfg Config) (*slog.Logger, error) {
	level, err := ParseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}
	out := cfg.Out
	if out == nil {
		out = os.Stderr
	}
	opts := &slog.HandlerOptions{Level: level}
	format := strings.ToLower(strings.TrimSpace(cfg.Format))
	var handler slog.Handler
	switch format {
	case "", "text":
		handler = slog.NewTextHandler(out, opts)
	case "json":
		handler = slog.NewJSONHandler(out, opts)
	default:
		return nil, fmt.Errorf("invalid log format %q (want text or json)", cfg.Format)
	}
	return slog.New(handler), nil
}

// Setup installs a logger as slog's default. Subsequent slog.Info/Debug
// calls (from any package) use this configuration.
func Setup(cfg Config) (*slog.Logger, error) {
	l, err := New(cfg)
	if err != nil {
		return nil, err
	}
	slog.SetDefault(l)
	return l, nil
}
