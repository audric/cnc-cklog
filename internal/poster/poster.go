package poster

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/audric/cnc-cklog/internal/config"
	"github.com/audric/cnc-cklog/internal/store"
)

const (
	queueSize  = 512
	httpTimeout = 10 * time.Second
)

// Poster asynchronously POSTs batches of log lines to a URL as JSON.
type Poster struct {
	lc      *config.LogConfig
	queue   chan []store.LogLine
	done    chan struct{}
	client  *http.Client
}

func New(lc *config.LogConfig) *Poster {
	p := &Poster{
		lc:    lc,
		queue: make(chan []store.LogLine, queueSize),
		done:  make(chan struct{}),
		client: &http.Client{Timeout: httpTimeout},
	}
	go p.loop()
	return p
}

// Send enqueues lines for posting. Non-blocking; drops if queue is full.
func (p *Poster) Send(lines []store.LogLine) {
	// copy so the caller can reuse its slice
	cp := make([]store.LogLine, len(lines))
	copy(cp, lines)
	select {
	case p.queue <- cp:
	default:
		slog.Warn("poster queue full, dropping batch", "url", p.lc.APIURL, "count", len(lines))
	}
}

// Close drains the queue and shuts down the worker.
func (p *Poster) Close() {
	close(p.done)
}

func (p *Poster) loop() {
	for {
		select {
		case lines := <-p.queue:
			p.post(lines)
		case <-p.done:
			// drain remaining
			for {
				select {
				case lines := <-p.queue:
					p.post(lines)
				default:
					return
				}
			}
		}
	}
}

func (p *Poster) post(lines []store.LogLine) {
	payload := make([]map[string]string, 0, len(lines))
	for _, l := range lines {
		obj := make(map[string]string, len(p.lc.Columns))
		for i, col := range p.lc.Columns {
			if i < len(l.Fields) {
				obj[col] = l.Fields[i]
			}
		}
		payload = append(payload, obj)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		slog.Error("poster: marshal failed", "err", err)
		return
	}

	req, err := http.NewRequest(http.MethodPost, p.lc.APIURL, bytes.NewReader(body))
	if err != nil {
		slog.Error("poster: build request failed", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	switch p.lc.APIAuthType {
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+p.lc.APIAuthToken)
	case "basic":
		req.SetBasicAuth(p.lc.APIAuthUser, p.lc.APIAuthToken)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		slog.Warn("poster: POST failed", "url", p.lc.APIURL, "err", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		slog.Warn("poster: unexpected status", "url", p.lc.APIURL, "status", resp.StatusCode)
	} else {
		slog.Debug("poster: sent", "url", p.lc.APIURL, "count", len(lines))
	}
}
