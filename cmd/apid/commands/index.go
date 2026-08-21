package commands

import (
	"github.com/spf13/cobra"
)

var configPath string

var cmdRoot = &cobra.Command{
	Use:   "apid",
	Short: "apid",
	Long:  `apid - канал-CRUD оркестрация между farcd и mediamtx (.scratch/live-page/spec.md).`,
	Run:   doByDefault,
}

func init() {
	cmdRoot.Flags().StringVarP(&configPath, "config", "c", "apid.config.json", "path to apid's JSON config file")
}

func Execute() {
	cobra.CheckErr(cmdRoot.Execute())
}
