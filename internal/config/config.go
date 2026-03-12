package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/ini.v1"
)

var validCol = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

const defaultMaxFields = 10

type LogConfig struct {
	Name        string   // ini section name
	File        string   // path to log file
	MaxFields   int      // number of CSV fields to store
	Columns     []string // column names, len == MaxFields
	APIURL      string   // optional URL to POST lines to
	APIAuthType string   // "bearer", "basic", or "" (none)
	APIAuthToken string  // bearer token, or basic password
	APIAuthUser  string  // basic auth username

	// FOCAS2 fields — used by focas-logger, ignored by cklogd.
	// If FOCASHost is empty, FOCAS polling is disabled for this log.
	FOCASHost    string        // focas_host: controller IP address
	FOCASPort    int           // focas_port: default 8193
	MachineIP    string        // machine_ip: IP written into log lines (defaults to FOCASHost)
	MachineName  string        // machine_name: identifier written into log lines (defaults to uppercase section name)
	PollInterval time.Duration // poll_interval: how often to query the controller (default 2s)

	// Mazak DPRNT fields — used by mazak-logger, ignored by cklogd.
	// If DPRNTPath is empty, Mazak DPRNT forwarding is disabled for this log.
	// DPRNTPath may point to:
	//   - a directory: mazak-logger processes new PRNT*.DAT files as they appear
	//   - a single file: mazak-logger tails it for new lines
	DPRNTPath string // dprnt_path: local path to mounted SMB share (file or directory)
	DPRNTGlob string // dprnt_glob: filename pattern when DPRNTPath is a directory (default: PRNT*.DAT)
}

type Config struct {
	DBDir        string `ini:"dbdir"`
	RetainMonths int    `ini:"retain_months"`
	Debug        bool   `ini:"debug"`
	Logs         []*LogConfig
}

func Default() *Config {
	return &Config{
		DBDir:        ".",
		RetainMonths: 24,
		Debug:        false,
	}
}

// Load reads the ini file at path into cfg.
func Load(path string, cfg *Config) error {
	f, err := ini.Load(path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}

	if err := f.Section("cklogd").MapTo(cfg); err != nil {
		return fmt.Errorf("parse [cklogd]: %w", err)
	}
	if cfg.RetainMonths < 1 {
		return fmt.Errorf("retain_months must be >= 1")
	}

	cfg.Logs = nil
	for _, sec := range f.Sections() {
		name := sec.Name()
		// skip DEFAULT, cklogd, and *.columns sections (handled below)
		if name == ini.DefaultSection || name == "cklogd" || strings.Contains(name, ".") {
			continue
		}

		lc := &LogConfig{
			Name:      name,
			MaxFields: defaultMaxFields,
		}
		if key, err := sec.GetKey("file"); err == nil {
			lc.File = strings.TrimSpace(key.Value())
		}
		if lc.File == "" {
			return fmt.Errorf("[%s] missing required key 'file'", name)
		}
		if key, err := sec.GetKey("api_url"); err == nil {
			lc.APIURL = strings.TrimSpace(key.Value())
		}
		if key, err := sec.GetKey("api_auth_type"); err == nil {
			t := strings.ToLower(strings.TrimSpace(key.Value()))
			if t != "bearer" && t != "basic" {
				return fmt.Errorf("[%s] api_auth_type must be 'bearer' or 'basic'", name)
			}
			lc.APIAuthType = t
		}
		if key, err := sec.GetKey("api_auth_token"); err == nil {
			lc.APIAuthToken = strings.TrimSpace(key.Value())
		}
		if key, err := sec.GetKey("api_auth_user"); err == nil {
			lc.APIAuthUser = strings.TrimSpace(key.Value())
		}
		if lc.APIAuthType != "" && lc.APIURL == "" {
			return fmt.Errorf("[%s] api_auth_type requires api_url to be set", name)
		}
		if lc.APIAuthType == "bearer" && lc.APIAuthToken == "" {
			return fmt.Errorf("[%s] api_auth_type = bearer requires api_auth_token", name)
		}
		if lc.APIAuthType == "basic" && (lc.APIAuthUser == "" || lc.APIAuthToken == "") {
			return fmt.Errorf("[%s] api_auth_type = basic requires api_auth_user and api_auth_token", name)
		}
		if key, err := sec.GetKey("max_fields"); err == nil {
			n, err := strconv.Atoi(strings.TrimSpace(key.Value()))
			if err != nil || n < 1 {
				return fmt.Errorf("[%s] max_fields must be a positive integer", name)
			}
			lc.MaxFields = n
		}

		lc.Columns = defaultColumns(lc.MaxFields)
		colSec := name + ".columns"
		if f.HasSection(colSec) {
			for _, key := range f.Section(colSec).Keys() {
				idx, err := strconv.Atoi(key.Name())
				if err != nil || idx < 1 || idx > lc.MaxFields {
					return fmt.Errorf("[%s] key %q must be an integer between 1 and %d", colSec, key.Name(), lc.MaxFields)
				}
				col := sanitize(key.Value())
				if col == "" {
					return fmt.Errorf("[%s] key %d has invalid column name %q", colSec, idx, key.Value())
				}
				lc.Columns[idx-1] = col
			}
			seen := make(map[string]bool)
			for _, col := range lc.Columns {
				if seen[col] {
					return fmt.Errorf("[%s] duplicate column name %q", colSec, col)
				}
				seen[col] = true
			}
		}

		// FOCAS2 (optional)
		if key, err := sec.GetKey("focas_host"); err == nil {
			lc.FOCASHost = strings.TrimSpace(key.Value())
		}
		if lc.FOCASHost != "" {
			lc.FOCASPort = 8193
			if key, err := sec.GetKey("focas_port"); err == nil {
				if n, err := strconv.Atoi(strings.TrimSpace(key.Value())); err == nil && n > 0 {
					lc.FOCASPort = n
				}
			}
			lc.MachineIP = lc.FOCASHost
			if key, err := sec.GetKey("machine_ip"); err == nil {
				lc.MachineIP = strings.TrimSpace(key.Value())
			}
			lc.MachineName = strings.ToUpper(name)
			if key, err := sec.GetKey("machine_name"); err == nil {
				lc.MachineName = strings.TrimSpace(key.Value())
			}
			lc.PollInterval = 2 * time.Second
			if key, err := sec.GetKey("poll_interval"); err == nil {
				if d, err := time.ParseDuration(strings.TrimSpace(key.Value())); err == nil && d > 0 {
					lc.PollInterval = d
				}
			}
		}

		// Mazak DPRNT (optional)
		if key, err := sec.GetKey("dprnt_path"); err == nil {
			lc.DPRNTPath = strings.TrimSpace(key.Value())
		}
		if lc.DPRNTPath != "" {
			lc.DPRNTGlob = "PRNT*.DAT"
			if key, err := sec.GetKey("dprnt_glob"); err == nil {
				if g := strings.TrimSpace(key.Value()); g != "" {
					lc.DPRNTGlob = g
				}
			}
		}

		cfg.Logs = append(cfg.Logs, lc)
	}

	if len(cfg.Logs) == 0 {
		return fmt.Errorf("no log sections defined; add at least one [name] section with a 'file' key")
	}
	return nil
}

func defaultColumns(n int) []string {
	cols := make([]string, n)
	for i := range cols {
		cols[i] = fmt.Sprintf("Column%d", i+1)
	}
	return cols
}

func sanitize(s string) string {
	s = strings.TrimSpace(s)
	if !validCol.MatchString(s) {
		return ""
	}
	return s
}
