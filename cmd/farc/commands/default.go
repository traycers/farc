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

	"github.com/traycers/farc/internal/config"
	"github.com/traycers/farc/internal/farcd"
	"github.com/traycers/farc/internal/levellog"
)

func doByDefault(cmd *cobra.Command, args []string) {
	_ = godotenv.Load()

	err := config.EnsureExists(configPath)
	if err != nil {
		levellog.New(log.Fatalf).Error("farcd: %v", err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		levellog.New(log.Fatalf).Error("farcd: %v", err)
	}

	logger := log.New(logOutput("farcd", cfg.LogDir), "", log.LstdFlags)
	llog := levellog.New(logger.Printf)

	f, err := farcd.New(cfg, configPath)
	if err != nil {
		levellog.New(logger.Fatalf).Error("farcd: %v", err)
	}
	f.SetLogger(logger.Printf)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	llog.Info("farcd: starting (http=%s ws=%s metrics=%s, %d storages, %d channels)",
		cfg.HTTP, cfg.WS, cfg.Metrics, len(cfg.Storages), len(cfg.Channels))
	err = f.Run(ctx)
	if err != nil {
		llog.Error("farcd: exited with error: %v", err)
		stop()     // explicit call since the deferred one is skipped by os.Exit below
		os.Exit(1) //nolint:gocritic // stop() was already called on the line above
	}
	llog.Info("farcd: stopped")
}

// logOutput is stderr alone, or stderr plus dir/service.log when dir is set
// (FARC_LOG_DIR) -- a failure to open the log file is a warning, not fatal,
// since a running archiver shouldn't refuse to start over a logging problem.
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
