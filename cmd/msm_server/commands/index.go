package commands

import (
	"github.com/spf13/cobra"
)

var cmdRoot = &cobra.Command{
	Use:   "msm_server",
	Short: "msm_server",
	Long: `msm_server - интеграция farcd с внешним msm/controller: реле событий farcd
(/events/ws) во внешний сервис msm (temp/msm/openapi.yaml) и обратный
приём /api/v1/archives/* от msm/controller (temp/controller/openapi.yaml),
транслируемый в HTTP-вызовы к farcd.`,
	Run: doByDefault,
}

func Execute() {
	cobra.CheckErr(cmdRoot.Execute())
}
