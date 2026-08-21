package commands

import (
	"github.com/spf13/cobra"
)

var configPath string

var cmdRoot = &cobra.Command{
	Use:   "hlsd",
	Short: "hlsd",
	Long:  `hlsd - HLS-воспроизведение архива farc (docs/docs/archive/12-hls-server.md).`,
	Run:   doByDefault,
}

func init() {
	cmdRoot.Flags().StringVarP(&configPath, "config", "c", "hlsd.config.json", "path to hlsd's JSON config file (docs/docs/archive/12-hls-server.md §7)")
}

func Execute() {
	cobra.CheckErr(cmdRoot.Execute())
}
