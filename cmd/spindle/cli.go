package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	spindle "github.com/alexph/spindle/internal/spindle"
	"github.com/spf13/cobra"
)

var runServer = spindle.Run

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "spindle",
		Short:         "Spindle server CLI",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.AddCommand(newRunCmd())
	return cmd
}

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run the Spindle server",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			fmt.Fprintln(cmd.ErrOrStderr(), "Spindle server starting...")
			if err := runServer(ctx); err != nil {
				if ctx.Err() != nil || errors.Is(err, context.Canceled) {
					return nil
				}
				return err
			}
			fmt.Fprintln(cmd.ErrOrStderr(), "Spindle server shut down.")
			return nil
		},
	}
}

func executeCLI(stdout, stderr io.Writer) error {
	cmd := newRootCmd()
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	return cmd.Execute()
}

func main() {
	if err := executeCLI(os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
