package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

// Flags are the command-line values that were explicitly set.
// A false Set leaves that field out of the overlay.
type Flags struct {
	Bind              string
	BindSet           bool
	DataDir           string
	DataDirSet        bool
	InsecurePlaintext bool
	InsecureSet       bool
	PIDFile           string
	PIDFileSet        bool
	TLSCert           string
	TLSCertSet        bool
	TLSKey            string
	TLSKeySet         bool
	TLSClientCA       string
	TLSClientCASet    bool
	LogLevel          string
	LogLevelSet       bool
	LogFormat         string
	LogFormatSet      bool
}

// Load builds the effective configuration.
// filePath empty skips the file. A missing explicit path is an error.
// Environment variables named GRAPHDB_<SECTION>_<FIELD> override the file.
func Load(filePath string, flags Flags) (Config, error) {
	cfg := Defaults()
	if filePath != "" {
		if err := applyFile(&cfg, filePath); err != nil {
			return Config{}, err
		}
	}
	if err := applyEnv(&cfg); err != nil {
		return Config{}, err
	}
	applyFlags(&cfg, flags)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LoadOptional loads path when it is non-empty, otherwise graphdb.toml in the
// working directory when that file exists.
func LoadOptional(path string, pathSet bool, flags Flags) (Config, string, error) {
	if pathSet {
		if path == "" {
			return Config{}, "", gerr.New(gerr.InvalidArgument, "config path is empty")
		}
		cfg, err := Load(path, flags)
		return cfg, path, err
	}
	if path != "" {
		cfg, err := Load(path, flags)
		return cfg, path, err
	}
	if _, err := os.Stat("graphdb.toml"); err == nil {
		cfg, err := Load("graphdb.toml", flags)
		return cfg, "graphdb.toml", err
	} else if err != nil && !os.IsNotExist(err) {
		return Config{}, "", gerr.Wrap(gerr.Unavailable, "stat graphdb.toml", err)
	}
	cfg, err := Load("", flags)
	return cfg, "", err
}

type fileServer struct {
	Bind              *string        `toml:"bind"`
	MaxConnections    *int           `toml:"max_connections"`
	ShutdownTimeout   *time.Duration `toml:"shutdown_timeout"`
	PIDFile           *string        `toml:"pid_file"`
	InsecurePlaintext *bool          `toml:"insecure_plaintext"`
	TLS               *fileTLS       `toml:"tls"`
}

type fileTLS struct {
	Cert     *string `toml:"cert"`
	Key      *string `toml:"key"`
	ClientCA *string `toml:"client_ca"`
}

type fileStorage struct {
	DataDir            *string        `toml:"data_dir"`
	PageCacheBytes     *int64         `toml:"page_cache_bytes"`
	CheckpointInterval *time.Duration `toml:"checkpoint_interval"`
	WALSegmentBytes    *int64         `toml:"wal_segment_bytes"`
	Fsync              *bool          `toml:"fsync"`
	TrashRetention     *time.Duration `toml:"trash_retention"`
}

type fileLimits struct {
	QueryTimeout      *time.Duration `toml:"query_timeout"`
	QueryMemoryBytes  *int64         `toml:"query_memory_bytes"`
	MaxTxn            *int           `toml:"max_txn"`
	TxnIdle           *time.Duration `toml:"txn_idle"`
	MaxRowsPerMessage *int           `toml:"max_rows_per_message"`
	MaxMessageBytes   *int           `toml:"max_message_bytes"`
}

type fileLog struct {
	Level  *string `toml:"level"`
	Format *string `toml:"format"`
}

type fileMetrics struct {
	Bind *string `toml:"bind"`
}

type fileConfig struct {
	Server  *fileServer  `toml:"server"`
	Storage *fileStorage `toml:"storage"`
	Limits  *fileLimits  `toml:"limits"`
	Auth    *struct{}    `toml:"auth"`
	Log     *fileLog     `toml:"log"`
	Metrics *fileMetrics `toml:"metrics"`
}

func applyFile(cfg *Config, path string) error {
	var raw fileConfig
	meta, err := toml.DecodeFile(path, &raw)
	if err != nil {
		return gerr.Wrap(gerr.InvalidArgument, "config file", err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return gerr.Newf(gerr.InvalidArgument, "unknown config key %s", undecoded[0].String())
	}
	if s := raw.Server; s != nil {
		setString(&cfg.Server.Bind, s.Bind)
		setInt(&cfg.Server.MaxConnections, s.MaxConnections)
		setDuration(&cfg.Server.ShutdownTimeout, s.ShutdownTimeout)
		setString(&cfg.Server.PIDFile, s.PIDFile)
		setBool(&cfg.Server.InsecurePlaintext, s.InsecurePlaintext)
		if t := s.TLS; t != nil {
			setString(&cfg.Server.TLS.Cert, t.Cert)
			setString(&cfg.Server.TLS.Key, t.Key)
			setString(&cfg.Server.TLS.ClientCA, t.ClientCA)
		}
	}
	if s := raw.Storage; s != nil {
		setString(&cfg.Storage.DataDir, s.DataDir)
		setInt64(&cfg.Storage.PageCacheBytes, s.PageCacheBytes)
		setDuration(&cfg.Storage.CheckpointInterval, s.CheckpointInterval)
		setInt64(&cfg.Storage.WALSegmentBytes, s.WALSegmentBytes)
		setBool(&cfg.Storage.Fsync, s.Fsync)
		setDuration(&cfg.Storage.TrashRetention, s.TrashRetention)
	}
	if s := raw.Limits; s != nil {
		setDuration(&cfg.Limits.QueryTimeout, s.QueryTimeout)
		setInt64(&cfg.Limits.QueryMemoryBytes, s.QueryMemoryBytes)
		setInt(&cfg.Limits.MaxTxn, s.MaxTxn)
		setDuration(&cfg.Limits.TxnIdle, s.TxnIdle)
		setInt(&cfg.Limits.MaxRowsPerMessage, s.MaxRowsPerMessage)
		setInt(&cfg.Limits.MaxMessageBytes, s.MaxMessageBytes)
	}
	if s := raw.Log; s != nil {
		setString(&cfg.Log.Level, s.Level)
		setString(&cfg.Log.Format, s.Format)
	}
	if s := raw.Metrics; s != nil {
		setString(&cfg.Metrics.Bind, s.Bind)
	}
	return nil
}

func applyEnv(cfg *Config) error {
	var err error
	lookup := func(key string) (string, bool) {
		v, ok := os.LookupEnv(key)
		return v, ok
	}
	if err = envString(lookup, "GRAPHDB_SERVER_BIND", &cfg.Server.Bind); err != nil {
		return err
	}
	if err = envInt(lookup, "GRAPHDB_SERVER_MAX_CONNECTIONS", &cfg.Server.MaxConnections); err != nil {
		return err
	}
	if err = envDuration(lookup, "GRAPHDB_SERVER_SHUTDOWN_TIMEOUT", &cfg.Server.ShutdownTimeout); err != nil {
		return err
	}
	if err = envString(lookup, "GRAPHDB_SERVER_PID_FILE", &cfg.Server.PIDFile); err != nil {
		return err
	}
	if err = envBool(lookup, "GRAPHDB_SERVER_INSECURE_PLAINTEXT", &cfg.Server.InsecurePlaintext); err != nil {
		return err
	}
	if err = envString(lookup, "GRAPHDB_SERVER_TLS_CERT", &cfg.Server.TLS.Cert); err != nil {
		return err
	}
	if err = envString(lookup, "GRAPHDB_SERVER_TLS_KEY", &cfg.Server.TLS.Key); err != nil {
		return err
	}
	if err = envString(lookup, "GRAPHDB_SERVER_TLS_CLIENT_CA", &cfg.Server.TLS.ClientCA); err != nil {
		return err
	}
	if err = envString(lookup, "GRAPHDB_STORAGE_DATA_DIR", &cfg.Storage.DataDir); err != nil {
		return err
	}
	if err = envInt64(lookup, "GRAPHDB_STORAGE_PAGE_CACHE_BYTES", &cfg.Storage.PageCacheBytes); err != nil {
		return err
	}
	if err = envDuration(lookup, "GRAPHDB_STORAGE_CHECKPOINT_INTERVAL", &cfg.Storage.CheckpointInterval); err != nil {
		return err
	}
	if err = envInt64(lookup, "GRAPHDB_STORAGE_WAL_SEGMENT_BYTES", &cfg.Storage.WALSegmentBytes); err != nil {
		return err
	}
	if err = envBool(lookup, "GRAPHDB_STORAGE_FSYNC", &cfg.Storage.Fsync); err != nil {
		return err
	}
	if err = envDuration(lookup, "GRAPHDB_STORAGE_TRASH_RETENTION", &cfg.Storage.TrashRetention); err != nil {
		return err
	}
	if err = envDuration(lookup, "GRAPHDB_LIMITS_QUERY_TIMEOUT", &cfg.Limits.QueryTimeout); err != nil {
		return err
	}
	if err = envInt64(lookup, "GRAPHDB_LIMITS_QUERY_MEMORY_BYTES", &cfg.Limits.QueryMemoryBytes); err != nil {
		return err
	}
	if err = envInt(lookup, "GRAPHDB_LIMITS_MAX_TXN", &cfg.Limits.MaxTxn); err != nil {
		return err
	}
	if err = envDuration(lookup, "GRAPHDB_LIMITS_TXN_IDLE", &cfg.Limits.TxnIdle); err != nil {
		return err
	}
	if err = envInt(lookup, "GRAPHDB_LIMITS_MAX_ROWS_PER_MESSAGE", &cfg.Limits.MaxRowsPerMessage); err != nil {
		return err
	}
	if err = envInt(lookup, "GRAPHDB_LIMITS_MAX_MESSAGE_BYTES", &cfg.Limits.MaxMessageBytes); err != nil {
		return err
	}
	if err = envString(lookup, "GRAPHDB_LOG_LEVEL", &cfg.Log.Level); err != nil {
		return err
	}
	if err = envString(lookup, "GRAPHDB_LOG_FORMAT", &cfg.Log.Format); err != nil {
		return err
	}
	if err = envString(lookup, "GRAPHDB_METRICS_BIND", &cfg.Metrics.Bind); err != nil {
		return err
	}
	return nil
}

func applyFlags(cfg *Config, f Flags) {
	if f.BindSet {
		cfg.Server.Bind = f.Bind
	}
	if f.DataDirSet {
		cfg.Storage.DataDir = f.DataDir
	}
	if f.InsecureSet {
		cfg.Server.InsecurePlaintext = f.InsecurePlaintext
	}
	if f.PIDFileSet {
		cfg.Server.PIDFile = f.PIDFile
	}
	if f.TLSCertSet {
		cfg.Server.TLS.Cert = f.TLSCert
	}
	if f.TLSKeySet {
		cfg.Server.TLS.Key = f.TLSKey
	}
	if f.TLSClientCASet {
		cfg.Server.TLS.ClientCA = f.TLSClientCA
	}
	if f.LogLevelSet {
		cfg.Log.Level = f.LogLevel
	}
	if f.LogFormatSet {
		cfg.Log.Format = f.LogFormat
	}
}

type lookupFunc func(string) (string, bool)

func envString(get lookupFunc, key string, dst *string) error {
	v, ok := get(key)
	if !ok {
		return nil
	}
	*dst = v
	return nil
}

func envBool(get lookupFunc, key string, dst *bool) error {
	v, ok := get(key)
	if !ok {
		return nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return gerr.Newf(gerr.InvalidArgument, "%s: %v", key, err)
	}
	*dst = b
	return nil
}

func envInt(get lookupFunc, key string, dst *int) error {
	v, ok := get(key)
	if !ok {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return gerr.Newf(gerr.InvalidArgument, "%s: %v", key, err)
	}
	*dst = n
	return nil
}

func envInt64(get lookupFunc, key string, dst *int64) error {
	v, ok := get(key)
	if !ok {
		return nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return gerr.Newf(gerr.InvalidArgument, "%s: %v", key, err)
	}
	*dst = n
	return nil
}

func envDuration(get lookupFunc, key string, dst *time.Duration) error {
	v, ok := get(key)
	if !ok {
		return nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return gerr.Newf(gerr.InvalidArgument, "%s: %v", key, err)
	}
	*dst = d
	return nil
}

func setString(dst *string, src *string) {
	if src != nil {
		*dst = *src
	}
}

func setBool(dst *bool, src *bool) {
	if src != nil {
		*dst = *src
	}
}

func setInt(dst *int, src *int) {
	if src != nil {
		*dst = *src
	}
}

func setInt64(dst *int64, src *int64) {
	if src != nil {
		*dst = *src
	}
}

func setDuration(dst *time.Duration, src *time.Duration) {
	if src != nil {
		*dst = *src
	}
}

// FormatError makes a config error readable on a terminal.
func FormatError(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(err))
}
