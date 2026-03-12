package reader

import (
	"bufio"
	"encoding/csv"
	"io"
	"log/slog"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/audric/cnc-cklog/internal/store"
)

const flushSize = 200

type FileReader struct {
	path       string
	offset     uint64
	inode      uint64
	store      *store.Store
	buf        []store.LogLine
	AfterFlush func([]store.LogLine) // optional; called after each successful flush
}

func New(path string, st *store.Store) *FileReader {
	return &FileReader{path: path, store: st}
}

// Init loads the stored offset/inode from the DB.
func (r *FileReader) Init() error {
	fo, err := r.store.GetOffset(r.path)
	if err != nil {
		return err
	}
	r.offset = fo.Offset
	r.inode = fo.Inode
	return nil
}

// ReadNew reads any new lines since the last offset and flushes them to the store.
func (r *FileReader) ReadNew() {
	info, err := os.Stat(r.path)
	if err != nil {
		slog.Warn("stat failed", "path", r.path, "err", err)
		return
	}

	currentInode := inode(info)
	currentSize := uint64(info.Size())

	// Rotation: inode changed or file was truncated
	if currentInode != r.inode || currentSize < r.offset {
		slog.Info("rotation detected, resetting offset", "path", r.path)
		r.offset = 0
		r.inode = currentInode
	}

	if currentSize == r.offset {
		return // nothing new
	}

	f, err := os.Open(r.path)
	if err != nil {
		slog.Warn("open failed", "path", r.path, "err", err)
		return
	}
	defer f.Close()

	if _, err := f.Seek(int64(r.offset), io.SeekStart); err != nil {
		slog.Warn("seek failed", "path", r.path, "err", err)
		return
	}

	scanner := bufio.NewScanner(f)
	now := time.Now().UTC()
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		r.buf = append(r.buf, store.LogLine{
			Filename:   r.path,
			Line:       line,
			Fields:     parseCSV(line),
			IngestedAt: now,
		})
		if len(r.buf) >= flushSize {
			r.flush(currentInode)
		}
	}

	// Update offset to current position
	pos, _ := f.Seek(0, io.SeekCurrent)
	r.offset = uint64(pos)

	if len(r.buf) > 0 {
		r.flush(currentInode)
	}
}

func (r *FileReader) Flush() {
	if len(r.buf) > 0 {
		r.flush(r.inode)
	}
}

// SetStore swaps the backing store (e.g. on monthly DB rotation).
// Caller must flush before calling this.
func (r *FileReader) SetStore(st *store.Store) {
	r.store = st
}

func (r *FileReader) flush(inode uint64) {
	if err := r.store.SaveBatch(r.buf, r.path, r.offset, inode); err != nil {
		slog.Error("flush failed", "path", r.path, "err", err)
		return
	}
	slog.Debug("flushed lines", "path", r.path, "count", len(r.buf))
	if r.AfterFlush != nil {
		flushed := make([]store.LogLine, len(r.buf))
		copy(flushed, r.buf)
		r.AfterFlush(flushed)
	}
	r.buf = r.buf[:0]
	r.inode = inode
}

func parseCSV(line string) []string {
	r := csv.NewReader(strings.NewReader(line))
	r.TrimLeadingSpace = true
	fields, err := r.Read()
	if err != nil || len(fields) <= 1 {
		return nil
	}
	return fields
}

func inode(info os.FileInfo) uint64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Ino
	}
	return 0
}
