// mazak-logger forwards Mazak DPRNT output to the log files watched by cklogd.
//
// The Mazak controller (Windows 2000, Mazatrol Matrix Nexus) writes DPRNT output
// to its local disk. That directory is shared via Windows SMB and mounted on the
// Linux server with mount.cifs. mazak-logger polls the mounted path and copies
// new lines to the log file.
//
// NOTE: inotify does not work on CIFS mounts. This process uses timed polling only.
//
// Two modes depending on dprnt_path:
//
//   Directory mode (dprnt_path points to a directory):
//     The Mazak creates a new file per POPEN/PCLOS cycle (PRNT001.DAT, PRNT002.DAT…).
//     mazak-logger picks up new files matching dprnt_glob and copies their lines.
//     Existing files present at startup are skipped.
//
//   File mode (dprnt_path points to a single file):
//     mazak-logger tails the file. Seeks to end on startup; reads new content on
//     each poll tick. Resets on file truncation or replacement (inode change).
//
// INI keys:
//
//	dprnt_path = /mnt/mazak/dprnt       ; mounted path — enables Mazak mode for this section
//	dprnt_glob = PRNT*.DAT              ; file pattern for directory mode (default: PRNT*.DAT)
//	poll_interval = 2s                  ; polling interval (default: 2s)
//
// Mazak NC program (EIA/ISO):
//
//	POPEN
//	DPRNT[START,*CNC1,*10.16.30.100,*#3011[4]-#3012[2]-#3013[2]*#3014[2]:#3015[2]]
//	PCLOS
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/audric/cnc-cklog/internal/config"
)

func main() {
	cfgPath := flag.String("config", "cklogd.ini", "path to ini configuration file")
	debug := flag.Bool("debug", false, "verbose logging")
	flag.Parse()

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	cfg := config.Default()
	if err := config.Load(*cfgPath, cfg); err != nil {
		slog.Error("failed to load config", "path", *cfgPath, "err", err)
		os.Exit(1)
	}

	var enabled []*config.LogConfig
	for _, lc := range cfg.Logs {
		if lc.DPRNTPath != "" {
			enabled = append(enabled, lc)
		}
	}
	if len(enabled) == 0 {
		slog.Error("no DPRNT-enabled sections found; add dprnt_path to at least one [name] section")
		os.Exit(1)
	}
	slog.Info("starting mazak-logger", "machines", len(enabled))

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for _, lc := range enabled {
		wg.Add(1)
		go func(lc *config.LogConfig) {
			defer wg.Done()
			runPoller(lc, stop)
		}(lc)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	slog.Info("shutting down")
	close(stop)
	wg.Wait()
	slog.Info("done")
}

// runPoller waits for dprnt_path to become available, then dispatches to the
// appropriate mode (directory or file). Retries on any error.
func runPoller(lc *config.LogConfig, stop <-chan struct{}) {
	log := slog.With("machine", lc.Name, "path", lc.DPRNTPath)

	interval := lc.PollInterval
	if interval == 0 {
		interval = 2 * time.Second
	}

	for {
		info, err := os.Stat(lc.DPRNTPath)
		if err != nil {
			log.Warn("dprnt_path not accessible, retrying in 10s", "err", err)
			if !sleep(stop, 10*time.Second) {
				return
			}
			continue
		}

		if info.IsDir() {
			log.Info("directory mode", "glob", lc.DPRNTGlob)
			pollDir(lc, interval, stop, log)
		} else {
			log.Info("file mode")
			pollFile(lc, lc.DPRNTPath, interval, stop, log)
		}

		// If we reach here the poller exited without a stop signal — retry.
		select {
		case <-stop:
			return
		case <-time.After(10 * time.Second):
		}
	}
}

// pollDir watches a directory for new files matching lc.DPRNTGlob.
// Files already present at startup are recorded as seen and skipped.
// New files are read in full and their lines copied to the log.
func pollDir(lc *config.LogConfig, interval time.Duration, stop <-chan struct{}, log *slog.Logger) {
	seen := make(map[string]bool)

	// Baseline: mark existing files as already seen.
	if existing, err := globDir(lc.DPRNTPath, lc.DPRNTGlob); err == nil {
		for _, f := range existing {
			seen[f] = true
		}
		log.Debug("baselined existing files", "count", len(existing))
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			files, err := globDir(lc.DPRNTPath, lc.DPRNTGlob)
			if err != nil {
				log.Warn("glob error", "err", err)
				return
			}
			for _, path := range files {
				if seen[path] {
					continue
				}
				seen[path] = true
				log.Info("new DPRNT file", "file", filepath.Base(path))
				copyAllLines(lc, path, log)
			}
		}
	}
}

// pollFile tails a single file. Seeks to end on startup; on each tick reads
// any new content. Resets to offset 0 on file truncation or inode change
// (i.e. the Mazak replaced the file).
func pollFile(lc *config.LogConfig, path string, interval time.Duration, stop <-chan struct{}, log *slog.Logger) {
	// Start at end of file so we don't replay old content.
	var offset int64
	var baseInode uint64
	if info, err := os.Stat(path); err == nil {
		offset = info.Size()
		baseInode = inode(info)
		log.Debug("starting at end of file", "offset", offset)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			info, err := os.Stat(path)
			if err != nil {
				log.Warn("stat error, waiting", "err", err)
				return
			}

			currentInode := inode(info)
			currentSize := info.Size()

			// File was replaced or truncated — reset.
			if currentInode != baseInode || currentSize < offset {
				log.Info("file reset detected, reading from start")
				offset = 0
				baseInode = currentInode
			}

			if currentSize == offset {
				continue // nothing new
			}

			newOffset, err := readLines(lc, path, offset, log)
			if err != nil {
				log.Warn("read error", "err", err)
				return
			}
			offset = newOffset
		}
	}
}

// copyAllLines reads an entire file and copies every non-empty line to the log.
func copyAllLines(lc *config.LogConfig, path string, log *slog.Logger) {
	_, err := readLines(lc, path, 0, log)
	if err != nil {
		log.Warn("failed to copy file", "file", filepath.Base(path), "err", err)
	}
}

// readLines reads new content in path starting at offset, writes each complete
// non-empty line to the log file, and returns the new offset.
func readLines(lc *config.LogConfig, path string, offset int64, log *slog.Logger) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return offset, err
	}
	defer f.Close()

	if _, err := f.Seek(offset, 0); err != nil {
		return offset, err
	}

	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if err := appendLine(lc, line, log); err != nil {
			return offset, err
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		return offset, err
	}

	newOffset, err := f.Seek(0, 1) // current position
	if err != nil {
		return offset, err
	}

	if count > 0 {
		log.Debug("forwarded lines", "count", count)
	}
	return newOffset, nil
}

// appendLine writes a single line to the log file watched by cklogd.
func appendLine(lc *config.LogConfig, line string, log *slog.Logger) error {
	if err := os.MkdirAll(filepath.Dir(lc.File), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	f, err := os.OpenFile(lc.File, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, line)
	return err
}

// globDir returns files in dir matching pattern, sorted alphabetically.
func globDir(dir, pattern string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

// inode extracts the inode number from a FileInfo on Linux.
func inode(info os.FileInfo) uint64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Ino
	}
	return 0
}

// sleep waits for d or until stop is closed. Returns false if stopped.
func sleep(stop <-chan struct{}, d time.Duration) bool {
	select {
	case <-stop:
		return false
	case <-time.After(d):
		return true
	}
}
