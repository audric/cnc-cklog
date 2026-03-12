package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

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
