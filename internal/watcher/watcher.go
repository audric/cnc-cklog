package watcher

import (
	"log/slog"

	"github.com/fsnotify/fsnotify"
)

type Op int

const (
	OpWrite Op = iota
	OpCreate
	OpRemove
)

type Event struct {
	Path string
	Op   Op
}

type Watcher struct {
	fw     *fsnotify.Watcher
	Events chan Event
}

func New(dir string) (*Watcher, error) {
	return NewMulti([]string{dir})
}

func NewMulti(dirs []string) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	for _, dir := range dirs {
		if err := fw.Add(dir); err != nil {
			fw.Close()
			return nil, err
		}
	}
	w := &Watcher{
		fw:     fw,
		Events: make(chan Event, 64),
	}
	go w.loop()
	return w, nil
}

func (w *Watcher) Close() error {
	return w.fw.Close()
}

func (w *Watcher) loop() {
	defer close(w.Events)
	for {
		select {
		case ev, ok := <-w.fw.Events:
			if !ok {
				return
			}
			var op Op
			switch {
			case ev.Has(fsnotify.Create):
				op = OpCreate
			case ev.Has(fsnotify.Write):
				op = OpWrite
			case ev.Has(fsnotify.Remove), ev.Has(fsnotify.Rename):
				op = OpRemove
			default:
				continue
			}
			select {
			case w.Events <- Event{Path: ev.Name, Op: op}:
			default:
				slog.Warn("event channel full, dropping event", "path", ev.Name)
			}
		case err, ok := <-w.fw.Errors:
			if !ok {
				return
			}
			slog.Error("watcher error", "err", err)
		}
	}
}
