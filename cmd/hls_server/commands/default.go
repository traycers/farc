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

	err := hlsconfig.EnsureExists(configPath)
	if err != nil {
		log.Fatalf("hls_server: %v", err)
	}
	cfg, err := hlsconfig.Load(configPath)
	if err != nil {
		log.Fatalf("hls_server: %v", err)
	}

	h, err := hlsd.New(cfg, configPath)
	if err != nil {
		log.Fatalf("hls_server: %v", err)
	}
	h.SetLogger(log.Printf)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("hls_server: starting (http=%s, farcd=%s, %d seed channels -- reconciled live against farcd afterward, ADR-021)",
		cfg.HTTP, cfg.Farcd.HTTP, len(cfg.Channels))
	err = h.Run(ctx)
	if err != nil {
		log.Printf("hls_server: exited with error: %v", err)
		stop()     // explicit call since the deferred one is skipped by os.Exit below
		os.Exit(1) //nolint:gocritic // stop() was already called on the line above
	}
	log.Printf("hls_server: stopped")
}
