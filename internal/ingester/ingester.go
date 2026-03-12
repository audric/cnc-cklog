package ingester

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/audric/heros-cklog/internal/config"
	"github.com/audric/heros-cklog/internal/poster"
	"github.com/audric/heros-cklog/internal/reader"
	"github.com/audric/heros-cklog/internal/store"
	"github.com/audric/heros-cklog/internal/watcher"
)

var dbNameRe = regexp.MustCompile(`^(.+)_(\d{4})_(\d{2})\.db$`)

type entry struct {
	lc       *config.LogConfig
	reader   *reader.FileReader
	store    *store.Store
	poster   *poster.Poster // nil if no api_url configured
	activeDB string
}

type Ingester struct {
	cfg     *config.Config
	watcher *watcher.Watcher
	entries map[string]*entry // keyed by log file path
	mu      sync.Mutex
	done    chan struct{}
}

func New(cfg *config.Config) (*Ingester, error) {
	// Collect unique directories to watch.
	dirs := map[string]struct{}{}
	for _, lc := range cfg.Logs {
		dirs[filepath.Dir(lc.File)] = struct{}{}
	}
	dirList := make([]string, 0, len(dirs))
	for d := range dirs {
		dirList = append(dirList, d)
	}

	w, err := watcher.NewMulti(dirList)
	if err != nil {
		return nil, err
	}
	return &Ingester{
		cfg:     cfg,
		watcher: w,
		entries: make(map[string]*entry),
		done:    make(chan struct{}),
	}, nil
}

// ScanExisting processes lines written while the daemon was down.
func (g *Ingester) ScanExisting() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, lc := range g.cfg.Logs {
		e := g.openEntry(lc)
		e.reader.ReadNew()
	}
}

// Run starts the event loop. Blocks until Close is called.
func (g *Ingester) Run() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-g.done:
			g.mu.Lock()
			g.flushAll()
			g.mu.Unlock()
			return

		case ev, ok := <-g.watcher.Events:
			if !ok {
				return
			}
			g.mu.Lock()
			e, ok := g.entries[ev.Path]
			if ok {
				switch ev.Op {
				case watcher.OpCreate, watcher.OpWrite:
					e.reader.ReadNew()
				case watcher.OpRemove:
					slog.Info("log file removed", "path", ev.Path)
				}
			}
			g.mu.Unlock()

		case <-ticker.C:
			g.mu.Lock()
			g.checkRotations()
			g.flushAll()
			g.mu.Unlock()
		}
	}
}

func (g *Ingester) Close() {
	close(g.done)
	g.watcher.Close()
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, e := range g.entries {
		e.store.Close()
		if e.poster != nil {
			e.poster.Close()
		}
	}
}

// openEntry creates and registers an entry for a log config. Caller holds mu.
func (g *Ingester) openEntry(lc *config.LogConfig) *entry {
	if e, ok := g.entries[lc.File]; ok {
		return e
	}
	dbPath := g.monthlyDBPath(lc, time.Now())
	st := g.mustOpenStore(dbPath, lc)
	r := reader.New(lc.File, st)
	if err := r.Init(); err != nil {
		slog.Warn("could not load offset", "path", lc.File, "err", err)
	}
	e := &entry{lc: lc, reader: r, store: st, activeDB: dbPath}
	if lc.APIURL != "" {
		e.poster = poster.New(lc)
		r.AfterFlush = e.poster.Send
		slog.Info("API posting enabled", "path", lc.File, "url", lc.APIURL)
	}
	g.entries[lc.File] = e
	slog.Info("tracking log file", "path", lc.File, "db", dbPath)
	return e
}

// checkRotations detects month changes and rotates DBs. Caller holds mu.
func (g *Ingester) checkRotations() {
	now := time.Now()
	for _, e := range g.entries {
		expected := g.monthlyDBPath(e.lc, now)
		if expected == e.activeDB {
			continue
		}
		slog.Info("rotating DB", "path", e.lc.File, "old", e.activeDB, "new", expected)
		e.reader.Flush()
		e.store.Close()
		st := g.mustOpenStore(expected, e.lc)
		e.reader.SetStore(st)
		e.store = st
		e.activeDB = expected
		g.cleanupOldDBs(e.lc, now)
	}
}

func (g *Ingester) flushAll() {
	for _, e := range g.entries {
		e.reader.Flush()
	}
}

func (g *Ingester) monthlyDBPath(lc *config.LogConfig, t time.Time) string {
	return filepath.Join(g.cfg.DBDir, fmt.Sprintf("%s_%s.db", lc.Name, t.Format("2006_01")))
}

func (g *Ingester) mustOpenStore(dbPath string, lc *config.LogConfig) *store.Store {
	st, err := store.Open(dbPath, lc.Columns)
	if err != nil {
		slog.Error("failed to open store", "db", dbPath, "err", err)
	}
	return st
}

// cleanupOldDBs removes monthly DB files older than RetainMonths. Caller holds mu.
func (g *Ingester) cleanupOldDBs(lc *config.LogConfig, now time.Time) {
	cutoff := now.AddDate(0, -g.cfg.RetainMonths, 0)
	pattern := filepath.Join(g.cfg.DBDir, lc.Name+"_*.db")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return
	}
	for _, f := range matches {
		base := filepath.Base(f)
		m := dbNameRe.FindStringSubmatch(base)
		if m == nil {
			continue
		}
		// parse YYYY_MM from filename
		t, err := time.Parse("2006_01", m[2]+"_"+m[3])
		if err != nil {
			continue
		}
		if t.Before(cutoff) {
			slog.Info("removing old DB", "file", f)
			if err := os.Remove(f); err != nil {
				slog.Warn("failed to remove old DB", "file", f, "err", err)
			}
			// also remove WAL/SHM sidecar files
			os.Remove(f + "-wal")
			os.Remove(f + "-shm")
		}
	}
}

// WatchedPaths returns the file paths being tracked (for logging).
func (g *Ingester) WatchedPaths() []string {
	paths := make([]string, 0, len(g.cfg.Logs))
	for _, lc := range g.cfg.Logs {
		paths = append(paths, lc.File)
	}
	return paths
}

