package commands

import (
	"github.com/spf13/cobra"
)

var cmdRoot = &cobra.Command{
	Use:   "msm_server",
	Short: "msm_server",
	Long:  `msm_server - реле событий farcd (/events/ws) во внешний сервис msm (temp/msm/openapi.yaml).`,
	Run:   doByDefault,
}

func Execute() {
	cobra.CheckErr(cmdRoot.Execute())
}
