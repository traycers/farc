package commands

import (
	"github.com/spf13/cobra"
)

var cmdRoot = &cobra.Command{
	Use:   "arch",
	Short: "arch",
	Long:  `arch - это видео-архив.`,
	Run:   doByDefault,
}

func init() {
	// cmdRoot.AddCommand(cmd_readme.CmdReadme)
}

func Execute() {
	cobra.CheckErr(cmdRoot.Execute())
}
