package commands

import (
	"fmt"
	"os"
	"traycers/farc/cmd/arch/config"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

func doByDefault(cmd *cobra.Command, args []string) {
	_ = godotenv.Load()
	_, err := config.NewConfig()
	if err != nil {
		fmt.Println("config reading failed: ", err)
		os.Exit(400)
	}
	fmt.Println("hello world!")
}
