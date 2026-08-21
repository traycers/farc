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

	"github.com/traycers/farc/internal/apid"
	"github.com/traycers/farc/internal/apidconfig"
	"github.com/traycers/farc/internal/levellog"
)

func doByDefault(cmd *cobra.Command, args []string) {
	_ = godotenv.Load()

	err := apidconfig.EnsureExists(configPath)
	if err != nil {
		levellog.New(log.Fatalf).Error("apid: %v", err)
	}
	cfg, err := apidconfig.Load(configPath)
	if err != nil {
		levellog.New(log.Fatalf).Error("apid: %v", err)
	}

	logger := log.New(logOutput("apid", cfg.LogDir), "", log.LstdFlags)
	llog := levellog.New(logger.Printf)

	a := apid.New(cfg)
	a.SetLogger(logger.Printf)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	llog.Info("apid: starting (http=%s metrics=%s, farcd=%s, mediamtx=%s)",
		cfg.HTTP, cfg.Metrics, cfg.Farcd.HTTP, cfg.Mediamtx.APIBase)
	err = a.Run(ctx)
	if err != nil {
		llog.Error("apid: exited with error: %v", err)
		stop()     // explicit call since the deferred one is skipped by os.Exit below
		os.Exit(1) //nolint:gocritic // stop() was already called on the line above
	}
	llog.Info("apid: stopped")
}

// logOutput is stderr alone, or stderr plus dir/service.log when dir is set
// (APID_LOG_DIR) -- a failure to open the log file is a warning, not
// fatal, matching cmd/farc's/cmd/hlsd's identical helper.
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
