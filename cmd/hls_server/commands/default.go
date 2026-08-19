package commands

import (
	"context"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"

	"github.com/traycers/farc/internal/hlsconfig"
	"github.com/traycers/farc/internal/hlsd"
	"github.com/traycers/farc/internal/levellog"
)

func doByDefault(cmd *cobra.Command, args []string) {
	_ = godotenv.Load()

	err := hlsconfig.EnsureExists(configPath)
	if err != nil {
		levellog.New(log.Fatalf).Error("hls_server: %v", err)
	}
	cfg, err := hlsconfig.Load(configPath)
	if err != nil {
		levellog.New(log.Fatalf).Error("hls_server: %v", err)
	}

	logger := log.New(logOutput("hls_server", cfg.LogDir), "", log.LstdFlags)
	llog := levellog.New(logger.Printf)

	h, err := hlsd.New(cfg, configPath)
	if err != nil {
		levellog.New(logger.Fatalf).Error("hls_server: %v", err)
	}
	h.SetLogger(logger.Printf)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	llog.Info("hls_server: starting (http=%s metrics=%s, farcd=%s, %d seed channels -- reconciled live against farcd afterward, ADR-021)",
		cfg.HTTP, cfg.Metrics, cfg.Farcd.HTTP, len(cfg.Channels))
	err = h.Run(ctx)
	if err != nil {
		llog.Error("hls_server: exited with error: %v", err)
		stop()     // explicit call since the deferred one is skipped by os.Exit below
		os.Exit(1) //nolint:gocritic // stop() was already called on the line above
	}
	llog.Info("hls_server: stopped")
}

// logOutput is stderr alone, or stderr plus dir/service.log when dir is set
// (HLS_SERVER_LOG_DIR) -- a failure to open the log file is a warning, not
// fatal, since a running player backend shouldn't refuse to start over a
// logging problem.
func logOutput(service, dir string) io.Writer {
	if dir == "" {
		return os.Stderr
	}
	err := os.MkdirAll(dir, 0o750)
	if err != nil {
		levellog.New(log.Printf).Warn("%s: log dir %s: %v (logging to stderr only)", service, dir, err)
		return os.Stderr
	}
	f, err := os.OpenFile(filepath.Join(dir, service+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		levellog.New(log.Printf).Warn("%s: open log file in %s: %v (logging to stderr only)", service, dir, err)
		return os.Stderr
	}
	return io.MultiWriter(os.Stderr, f)
}
