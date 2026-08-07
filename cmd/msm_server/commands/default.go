package commands

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"

	"traycers/farc/internal/msmconfig"
	"traycers/farc/internal/msmd"
)

func doByDefault(cmd *cobra.Command, args []string) {
	_ = godotenv.Load()

	cfg, err := msmconfig.Load()
	if err != nil {
		log.Fatalf("msm_server: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("msm_server: starting (farcd ws=%s, farcd http=%s, msm=%s)", cfg.FarcWS, cfg.FarcHTTP, cfg.MSMBaseURL)
	msmd.Run(ctx, cfg, log.Printf)
	log.Printf("msm_server: stopped")
}
