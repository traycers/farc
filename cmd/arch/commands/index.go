package commands

import (
	"github.com/spf13/cobra"
)

var configPath string

var cmdRoot = &cobra.Command{
	Use:   "arch",
	Short: "arch",
	Long:  `arch - это видео-архив.`,
	Run:   doByDefault,
}

func init() {
	cmdRoot.Flags().StringVarP(&configPath, "config", "c", "farc.config.json", "path to farcd's JSON config file (docs/docs/archive/04-storage-operations.md §2.1)")
}

func Execute() {
	cobra.CheckErr(cmdRoot.Execute())
}
