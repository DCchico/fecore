package cmd

import (
	"fmt"

	"github.com/morikuni/aec"
	"github.com/spf13/cobra"
)

// WelcomeMessage to introduce ofc-bootstrap
const WelcomeMessage = "Welcome to FaasEdge-core or (core)"

func init() {
	rootCommand.AddCommand(versionCmd)
	rootCommand.AddCommand(upCmd)
	rootCommand.AddCommand(installCmd)
	rootCommand.AddCommand(makeProviderCmd())
	rootCommand.AddCommand(collectCmd)
}

func RootCommand() *cobra.Command {
	return rootCommand
}

var (
	// GitCommit Git Commit SHA
	GitCommit string
	// Version version of the CLI
	Version string
)

// Execute core
func Execute(version, gitCommit string) error {

	// Get Version and GitCommit values from main.go.
	Version = version
	GitCommit = gitCommit

	if err := rootCommand.Execute(); err != nil {
		return err
	}
	return nil
}

var rootCommand = &cobra.Command{
	Use:   "core",
	Short: "Start core",
	Long: `
faasedge-core - Serverless For Everyone Else

Learn how to build, secure, and monitor functions with core with 
the eBook:

https://gumroad.com/l/serverless-for-everyone-else
`,
	RunE:         runRootCommand,
	SilenceUsage: true,
}

func runRootCommand(cmd *cobra.Command, args []string) error {

	printLogo()
	cmd.Help()

	return nil
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Display version information.",
	Run:   parseBaseCommand,
}

func parseBaseCommand(_ *cobra.Command, _ []string) {
	printLogo()

	printVersion()
}

func printVersion() {
	fmt.Printf("core version: %s\tcommit: %s\n", GetVersion(), GitCommit)
}

func printLogo() {
	logoText := aec.WhiteF.Apply(Logo)
	fmt.Println(logoText)
}

// GetVersion get latest version
func GetVersion() string {
	if len(Version) == 0 {
		return "dev"
	}
	return Version
}

// Logo for version and root command
const Logo = `
 _____                        _                   ____               
|  ___|_ _  __ _ ___  ___  __| | __ _  ___       / ___|___  _ __ ___ 
| |_ / _ |/ _  / __|/ _ \/ _ |/ _ |/ _ \__| |   / _ \| '__/ _ \
|  _| (_| | (_| \__ \  __/ (_| | (_| |  __/_____| |__| (_) | | |  __/
|_|  \__,_|\__,_|___/\___|\__,_|\__, |\___|      \____\___/|_|  \___|
                                |___/                                
`
