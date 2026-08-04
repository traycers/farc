package commands

import (
	"github.com/spf13/cobra"
)

var configPath string

var cmdRoot = &cobra.Command{
	Use:   "hls_server",
	Short: "hls_server",
	Long:  `hls_server - HLS-воспроизведение архива farc (docs/docs/archive/12-hls-server.md).`,
	Run:   doByDefault,
}

func init() {
	cmdRoot.Flags().StringVarP(&configPath, "config", "c", "hls_server.config.json", "path to hls_server's JSON config file (docs/docs/archive/12-hls-server.md §7)")
}

func Execute() {
	cobra.CheckErr(cmdRoot.Execute())
}
