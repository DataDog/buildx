package commands

import (
	"context"

	"github.com/docker/buildx/store"
	"github.com/docker/buildx/store/storeutil"
	"github.com/docker/cli/cli"
	"github.com/docker/cli/cli/command"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/spf13/cobra"
)

type rmOptions struct {
	builder       string
	keepState     bool
	keepBuildkitd bool
}

func runRm(dockerCli command.Cli, in rmOptions) error {
	ctx := appcontext.Context()

	txn, release, err := storeutil.GetStore(dockerCli)
	if err != nil {
		return err
	}
	defer release()

	if in.builder != "" {
		ng, err := storeutil.GetNodeGroup(txn, dockerCli, in.builder)
		if err != nil {
			return err
		}
		err1 := rm(ctx, dockerCli, ng, in.keepState, in.keepBuildkitd)
		if err := txn.Remove(ng.Name); err != nil {
			return err
		}
		return err1
	}

	ng, err := storeutil.GetCurrentInstance(txn, dockerCli)
	if err != nil {
		return err
	}
	if ng != nil {
		err1 := rm(ctx, dockerCli, ng, in.keepState, in.keepBuildkitd)
		if err := txn.Remove(ng.Name); err != nil {
			return err
		}
		return err1
	}

	return nil
}

func rmCmd(dockerCli command.Cli, rootOpts *rootOptions) *cobra.Command {
	var options rmOptions

	cmd := &cobra.Command{
		Use:   "rm [NAME]",
		Short: "Remove a builder instance",
		Args:  cli.RequiresMaxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			options.builder = rootOpts.builder
			if len(args) > 0 {
				options.builder = args[0]
			}
			return runRm(dockerCli, options)
		},
	}

	flags := cmd.Flags()
	flags.BoolVar(&options.keepState, "keep-state", false, "Keep BuildKit state")
	flags.BoolVar(&options.keepBuildkitd, "keep-buildkitd", false, "Keep the buildkitd daemon running")

	return cmd
}

func rm(ctx context.Context, dockerCli command.Cli, ng *store.NodeGroup, keepState, keepBuildkitd bool) error {
	dis, err := driversForNodeGroup(ctx, dockerCli, ng, "")
	if err != nil {
		return err
	}
	for _, di := range dis {
		if di.Driver == nil {
			continue
		}
		// Do not stop the buildkitd daemon when --keep-buildkitd is provided
		if !keepBuildkitd {
			if err := di.Driver.Stop(ctx, true); err != nil {
				return err
			}
		}
		if err := di.Driver.Rm(ctx, true, !keepState, !keepBuildkitd); err != nil {
			return err
		}
		if di.Err != nil {
			err = di.Err
		}
	}
	return err
}
