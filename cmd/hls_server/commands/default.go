package commands

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"

	"traycers/farc/internal/hlsconfig"
	"traycers/farc/internal/hlsd"
)

func doByDefault(cmd *cobra.Command, args []string) {
	_ = godotenv.Load()

	cfg, err := hlsconfig.Load(configPath)
	if err != nil {
		log.Fatalf("hls_server: %v", err)
	}

	h, err := hlsd.New(cfg)
	if err != nil {
		log.Fatalf("hls_server: %v", err)
	}
	h.SetLogger(log.Printf)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("hls_server: starting (http=%s, %d farcd endpoints, %d channels)",
		cfg.HTTP, len(cfg.Farcds), len(cfg.Channels))
	if err := h.Run(ctx); err != nil {
		log.Printf("hls_server: exited with error: %v", err)
		os.Exit(1)
	}
	log.Printf("hls_server: stopped")
}
