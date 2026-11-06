// Command axai turns a natural-language request into AxonOps configuration.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/axonops/axai/internal/cli"
)

func main() { os.Exit(run()) }

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root := cli.New(cli.DefaultEnv())
	if err := root.ExecuteContext(ctx); err != nil {
		if msg := err.Error(); msg != "" && msg != fmt.Sprintf("exit %d", cli.ExitCode(err)) {
			fmt.Fprintln(os.Stderr, "axai:", msg)
		}
		return cli.ExitCode(err)
	}
	return 0
}
