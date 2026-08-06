package commands

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"

	"traycers/farc/internal/config"
	"traycers/farc/internal/farcd"
)

func doByDefault(cmd *cobra.Command, args []string) {
	_ = godotenv.Load()

	err := config.EnsureExists(configPath)
	if err != nil {
		log.Fatalf("farcd: %v", err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("farcd: %v", err)
	}

	f, err := farcd.New(cfg, configPath)
	if err != nil {
		log.Fatalf("farcd: %v", err)
	}
	f.SetLogger(log.Printf)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("farcd: starting (http=%s ws=%s metrics=%s, %d storages, %d channels)",
		cfg.HTTP, cfg.WS, cfg.Metrics, len(cfg.Storages), len(cfg.Channels))
	err = f.Run(ctx)
	if err != nil {
		log.Printf("farcd: exited with error: %v", err)
		stop()     // explicit call since the deferred one is skipped by os.Exit below
		os.Exit(1) //nolint:gocritic // stop() was already called on the line above
	}
	log.Printf("farcd: stopped")
}
