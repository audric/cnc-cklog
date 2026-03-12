// focas-logger polls Fanuc CNC controllers via FOCAS2 and writes START/END
// events to the log files watched by cklogd.
//
// It reads the same cklogd.ini file as cklogd. Any [name] section with a
// focas_host key is polled; sections without focas_host are ignored.
//
// INI keys (all optional except focas_host):
//
//	focas_host    = 10.16.30.100   # controller IP — enables FOCAS for this section
//	focas_port    = 8193           # FOCAS2 port (default: 8193)
//	machine_ip    = 10.16.30.100   # IP written into CSV lines (default: focas_host)
//	machine_name  = CNC1           # identifier written into CSV lines (default: uppercase section name)
//	poll_interval = 2s             # polling interval (default: 2s)
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/audric/cnc-cklog/internal/config"
	"github.com/audric/cnc-cklog/internal/focas"
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

	// Filter to only FOCAS-enabled log sections.
	var enabled []*config.LogConfig
	for _, lc := range cfg.Logs {
		if lc.FOCASHost != "" {
			enabled = append(enabled, lc)
		}
	}
	if len(enabled) == 0 {
		slog.Error("no FOCAS-enabled sections found; add focas_host to at least one [name] section in the config")
		os.Exit(1)
	}
	slog.Info("starting focas-logger", "machines", len(enabled))

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

// runPoller is the outer reconnect loop for one machine.
// It restarts the poll session automatically on any error.
func runPoller(lc *config.LogConfig, stop <-chan struct{}) {
	log := slog.With("machine", lc.Name, "host", lc.FOCASHost)
	for {
		if err := poll(lc, stop, log); err != nil {
			log.Warn("connection error, retrying in 10s", "err", err)
		} else {
			return // clean shutdown via stop channel
		}
		select {
		case <-stop:
			return
		case <-time.After(10 * time.Second):
		}
	}
}

// poll opens one FOCAS2 session and runs the poll loop until error or stop.
func poll(lc *config.LogConfig, stop <-chan struct{}, log *slog.Logger) error {
	log.Info("connecting")
	client, err := focas.Connect(lc.FOCASHost, lc.FOCASPort, 10)
	if err != nil {
		return err
	}
	defer client.Close()
	log.Info("connected")

	ticker := time.NewTicker(lc.PollInterval)
	defer ticker.Stop()

	// Silent first poll: establish baseline state without emitting an event.
	// This avoids a spurious START if the machine is already running when we connect.
	wasRunning, err := client.IsRunning()
	if err != nil {
		return err
	}
	var startProg string // program name captured at START time
	if wasRunning {
		startProg, _ = client.ProgramName()
		log.Debug("machine already running on connect", "prog", startProg)
	}

	for {
		select {
		case <-stop:
			return nil
		case <-ticker.C:
			running, err := client.IsRunning()
			if err != nil {
				return err
			}

			switch {
			case running && !wasRunning:
				// Transition: idle → running
				prog, _ := client.ProgramName()
				if prog == "" {
					prog = lc.MachineName
				}
				startProg = prog
				writeEvent(lc, "START", prog, log)

			case !running && wasRunning:
				// Transition: running → idle
				writeEvent(lc, "END", startProg, log)
				startProg = ""
			}

			wasRunning = running
		}
	}
}

// writeEvent appends a single CSV event line to the log file.
func writeEvent(lc *config.LogConfig, event, program string, log *slog.Logger) {
	if program == "" {
		program = lc.MachineName
	}
	line := fmt.Sprintf("%s, %s, %s, %s\n",
		event,
		program,
		lc.MachineIP,
		time.Now().Format("2006-01-02 15:04"),
	)

	dir := filepath.Dir(lc.File)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Error("cannot create log directory", "dir", dir, "err", err)
		return
	}

	f, err := os.OpenFile(lc.File, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
	if err != nil {
		log.Error("cannot open log file", "path", lc.File, "err", err)
		return
	}
	defer f.Close()

	if _, err := f.WriteString(line); err != nil {
		log.Error("write failed", "path", lc.File, "err", err)
		return
	}
	log.Info("event written", "event", event, "program", program)
}
