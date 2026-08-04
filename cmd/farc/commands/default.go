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
	if err := f.Run(ctx); err != nil {
		log.Printf("farcd: exited with error: %v", err)
		os.Exit(1)
	}
	log.Printf("farcd: stopped")
}
