// Package config loads graphdb settings from defaults, a TOML file,
// GRAPHDB_* environment variables, and flags. Later sources win.
package config

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

const (
	// DefaultBind is the localhost gRPC address.
	DefaultBind = "127.0.0.1:9530"
	// DefaultDataDir is the directory that holds one subdirectory per database.
	DefaultDataDir = "data"
	// DefaultMetricsBind is recorded for Phase 8. This phase does not listen on it.
	DefaultMetricsBind = "127.0.0.1:9531"

	defaultMaxConnections   = 1000
	defaultPageCacheBytes   = 128 * 8192
	defaultWALSegmentBytes  = 64 << 20
	defaultQueryMemoryBytes = 8 << 20
	defaultMaxTxn           = 1000
	defaultMaxRows          = 256
	defaultMaxMessageBytes  = 4 << 20
)

// Config is the effective process configuration.
type Config struct {
	Server  Server  `toml:"server"`
	Storage Storage `toml:"storage"`
	Limits  Limits  `toml:"limits"`
	Auth    Auth    `toml:"auth"`
	Log     Log     `toml:"log"`
	Metrics Metrics `toml:"metrics"`
}

// Server is the listener and process lifetime.
type Server struct {
	Bind              string        `toml:"bind"`
	MaxConnections    int           `toml:"max_connections"`
	ShutdownTimeout   time.Duration `toml:"shutdown_timeout"`
	PIDFile           string        `toml:"pid_file"`
	InsecurePlaintext bool          `toml:"insecure_plaintext"`
	TLS               TLS           `toml:"tls"`
}

// TLS is the optional server certificate. ClientCA enables mTLS when set.
type TLS struct {
	Cert     string `toml:"cert"`
	Key      string `toml:"key"`
	ClientCA string `toml:"client_ca"`
}

// Storage is the data directory and the durability knobs.
type Storage struct {
	DataDir            string        `toml:"data_dir"`
	PageCacheBytes     int64         `toml:"page_cache_bytes"`
	CheckpointInterval time.Duration `toml:"checkpoint_interval"`
	WALSegmentBytes    int64         `toml:"wal_segment_bytes"`
	Fsync              bool          `toml:"fsync"`
	TrashRetention     time.Duration `toml:"trash_retention"`
}

// Limits caps connections, transactions, and one query.
type Limits struct {
	QueryTimeout      time.Duration `toml:"query_timeout"`
	QueryMemoryBytes  int64         `toml:"query_memory_bytes"`
	MaxTxn            int           `toml:"max_txn"`
	TxnIdle           time.Duration `toml:"txn_idle"`
	MaxRowsPerMessage int           `toml:"max_rows_per_message"`
	MaxMessageBytes   int           `toml:"max_message_bytes"`
}

// Auth is accepted and ignored until Phase 7.
type Auth struct{}

// Log selects the process logger.
type Log struct {
	Level  string `toml:"level"`
	Format string `toml:"format"`
}

// Metrics records the future Prometheus address. Nothing listens in this phase.
type Metrics struct {
	Bind string `toml:"bind"`
}

// Defaults returns the configuration used when no file, variable, or flag is set.
func Defaults() Config {
	return Config{
		Server: Server{
			Bind:            DefaultBind,
			MaxConnections:  defaultMaxConnections,
			ShutdownTimeout: 30 * time.Second,
		},
		Storage: Storage{
			DataDir:            DefaultDataDir,
			PageCacheBytes:     defaultPageCacheBytes,
			CheckpointInterval: 30 * time.Second,
			WALSegmentBytes:    defaultWALSegmentBytes,
			Fsync:              true,
			TrashRetention:     7 * 24 * time.Hour,
		},
		Limits: Limits{
			QueryTimeout:      30 * time.Second,
			QueryMemoryBytes:  defaultQueryMemoryBytes,
			MaxTxn:            defaultMaxTxn,
			TxnIdle:           5 * time.Minute,
			MaxRowsPerMessage: defaultMaxRows,
			MaxMessageBytes:   defaultMaxMessageBytes,
		},
		Log:     Log{Level: "info", Format: "text"},
		Metrics: Metrics{Bind: DefaultMetricsBind},
	}
}

// DefaultTOML is the commented file printed by `graphdb config print-defaults`.
func DefaultTOML() string {
	d := Defaults()
	return fmt.Sprintf(`# graphdb configuration. Flags override GRAPHDB_* environment variables,
# which override this file, which overrides built-in defaults.

[server]
bind = %q
max_connections = %d
shutdown_timeout = %q
pid_file = "" # empty uses <data_dir>/graphdb.pid
insecure_plaintext = false

[server.tls]
cert = ""
key = ""
client_ca = "" # set to require client certificates

[storage]
data_dir = %q
page_cache_bytes = %d
checkpoint_interval = %q
wal_segment_bytes = %d
fsync = true
trash_retention = %q

[limits]
query_timeout = %q
query_memory_bytes = %d
max_txn = %d
txn_idle = %q
max_rows_per_message = %d
max_message_bytes = %d

[auth]
# Authentication arrives in a later phase. This section is reserved.

[log]
level = %q
format = %q

[metrics]
bind = %q # not served until a later phase
`,
		d.Server.Bind,
		d.Server.MaxConnections,
		d.Server.ShutdownTimeout,
		d.Storage.DataDir,
		d.Storage.PageCacheBytes,
		d.Storage.CheckpointInterval,
		d.Storage.WALSegmentBytes,
		d.Storage.TrashRetention,
		d.Limits.QueryTimeout,
		d.Limits.QueryMemoryBytes,
		d.Limits.MaxTxn,
		d.Limits.TxnIdle,
		d.Limits.MaxRowsPerMessage,
		d.Limits.MaxMessageBytes,
		d.Log.Level,
		d.Log.Format,
		d.Metrics.Bind,
	)
}

// Validate checks the effective configuration. It does not open files.
func (c Config) Validate() error {
	if _, _, err := net.SplitHostPort(c.Server.Bind); err != nil {
		return gerr.Wrap(gerr.InvalidArgument, "server.bind", err)
	}
	if c.Server.MaxConnections < 1 {
		return gerr.New(gerr.InvalidArgument, "server.max_connections must be at least 1")
	}
	if c.Server.ShutdownTimeout <= 0 {
		return gerr.New(gerr.InvalidArgument, "server.shutdown_timeout must be positive")
	}
	if strings.TrimSpace(c.Storage.DataDir) == "" {
		return gerr.New(gerr.InvalidArgument, "storage.data_dir is empty")
	}
	if c.Storage.PageCacheBytes < 16*8192 {
		return gerr.New(gerr.InvalidArgument, "storage.page_cache_bytes must cover at least 16 pages")
	}
	if c.Storage.CheckpointInterval <= 0 {
		return gerr.New(gerr.InvalidArgument, "storage.checkpoint_interval must be positive")
	}
	if c.Storage.WALSegmentBytes < 64*1024 {
		return gerr.New(gerr.InvalidArgument, "storage.wal_segment_bytes is too small")
	}
	if c.Storage.TrashRetention < 0 {
		return gerr.New(gerr.InvalidArgument, "storage.trash_retention is negative")
	}
	if c.Limits.QueryTimeout <= 0 {
		return gerr.New(gerr.InvalidArgument, "limits.query_timeout must be positive")
	}
	if c.Limits.QueryMemoryBytes < 1 {
		return gerr.New(gerr.InvalidArgument, "limits.query_memory_bytes must be positive")
	}
	if c.Limits.MaxTxn < 1 {
		return gerr.New(gerr.InvalidArgument, "limits.max_txn must be at least 1")
	}
	if c.Limits.TxnIdle <= 0 {
		return gerr.New(gerr.InvalidArgument, "limits.txn_idle must be positive")
	}
	if c.Limits.MaxRowsPerMessage < 1 {
		return gerr.New(gerr.InvalidArgument, "limits.max_rows_per_message must be at least 1")
	}
	if c.Limits.MaxMessageBytes < 1024 {
		return gerr.New(gerr.InvalidArgument, "limits.max_message_bytes must be at least 1024")
	}
	switch strings.ToLower(c.Log.Level) {
	case "debug", "info", "warn", "warning", "error":
	default:
		return gerr.Newf(gerr.InvalidArgument, "log.level %q", c.Log.Level)
	}
	switch strings.ToLower(c.Log.Format) {
	case "text", "json":
	default:
		return gerr.Newf(gerr.InvalidArgument, "log.format %q", c.Log.Format)
	}
	if c.Metrics.Bind != "" {
		if _, _, err := net.SplitHostPort(c.Metrics.Bind); err != nil {
			return gerr.Wrap(gerr.InvalidArgument, "metrics.bind", err)
		}
	}
	tlsOn := c.Server.TLS.Cert != "" || c.Server.TLS.Key != "" || c.Server.TLS.ClientCA != ""
	if tlsOn && (c.Server.TLS.Cert == "" || c.Server.TLS.Key == "") {
		return gerr.New(gerr.InvalidArgument, "server.tls.cert and server.tls.key are both required")
	}
	if !loopback(c.Server.Bind) && !tlsOn && !c.Server.InsecurePlaintext {
		return gerr.New(gerr.InvalidArgument, "non-loopback bind requires TLS or --insecure-plaintext")
	}
	return nil
}

// PIDPath returns the pid file path, defaulting to <data_dir>/graphdb.pid.
func (c Config) PIDPath() string {
	if c.Server.PIDFile != "" {
		return c.Server.PIDFile
	}
	return filepath.Join(c.Storage.DataDir, "graphdb.pid")
}

// RedactedLines returns the effective settings for a startup banner.
// Certificate paths are paths, not file contents. fsync false is visible on purpose.
func (c Config) RedactedLines() []string {
	tls := "off"
	if c.Server.TLS.Cert != "" {
		tls = "on"
		if c.Server.TLS.ClientCA != "" {
			tls = "mtls"
		}
	}
	return []string{
		"bind=" + c.Server.Bind,
		"data_dir=" + c.Storage.DataDir,
		fmt.Sprintf("tls=%s", tls),
		fmt.Sprintf("fsync=%t", c.Storage.Fsync),
		fmt.Sprintf("max_connections=%d", c.Server.MaxConnections),
		fmt.Sprintf("max_txn=%d", c.Limits.MaxTxn),
		"query_timeout=" + c.Limits.QueryTimeout.String(),
		fmt.Sprintf("insecure_plaintext=%t", c.Server.InsecurePlaintext),
		"log=" + c.Log.Level + "/" + c.Log.Format,
	}
}

func loopback(bind string) bool {
	host, _, err := net.SplitHostPort(bind)
	if err != nil {
		host = bind
	}
	host = strings.Trim(host, "[]")
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
