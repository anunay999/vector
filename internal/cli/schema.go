package cli

import (
	_ "embed"
	"fmt"

	"github.com/spf13/cobra"
)

//go:embed config.schema.json
var configSchema string

func newSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Print the configuration JSON Schema",
		Long: "Prints a JSON Schema for config.yaml. Agents can use it to generate or\n" +
			"validate configuration programmatically.",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Print(configSchema)
		},
	}
}
