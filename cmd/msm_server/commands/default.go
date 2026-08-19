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

	"github.com/traycers/farc/internal/levellog"
	"github.com/traycers/farc/internal/msmconfig"
	"github.com/traycers/farc/internal/msmd"
)

func doByDefault(cmd *cobra.Command, args []string) {
	_ = godotenv.Load()

	cfg, err := msmconfig.Load()
	if err != nil {
		levellog.New(log.Fatalf).Error("msm_server: %v", err)
	}

	logger := log.New(logOutput("msm_server", cfg.LogDir), "", log.LstdFlags)
	llog := levellog.New(logger.Printf)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	llog.Info("msm_server: starting (http=%s metrics=%s, farcd ws=%s, farcd http=%s, msm=%s)", cfg.HTTP.String(), cfg.Metrics.String(), cfg.FarcWS, cfg.FarcHTTP, cfg.MSMBaseURL)
	msmd.Run(ctx, cfg, logger.Printf)
	llog.Info("msm_server: stopped")
}

// logOutput is stderr alone, or stderr plus dir/service.log when dir is set
// (MSM_SERVER_LOG_DIR) -- a failure to open the log file is a warning, not
// fatal, since msm_server shouldn't refuse to start over a logging problem.
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
