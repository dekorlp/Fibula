package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/dekorlp/fibula/client"
)

// printPendingConflicts reports conflict copies still on disk. They are the
// only trace of a decision nobody finished, so they are reported until they
// are gone (E53).
func printPendingConflicts(space *client.Space, out io.Writer) error {
	pending, err := space.PendingConflicts()
	if err != nil || len(pending) == 0 {
		return err
	}

	if _, err := fmt.Fprintf(out, "\n%d unresolved conflict(s), their version kept in %s:\n",
		len(pending), filepath.Join(client.SpaceDir, "conflicts")); err != nil {
		return err
	}
	for _, p := range pending {
		if _, err := fmt.Fprintf(out, "  %s\n", p); err != nil {
			return err
		}
	}
	return nil
}

func runSync(ctx context.Context, out io.Writer) error {
	space, ignore, err := openHere()
	if err != nil {
		return err
	}

	result, err := space.Sync(ctx, ignore)
	if err != nil {
		return err
	}
	refreshLockAttributes(ctx, space, ignore, out)

	if result.AlreadyCurrent {
		_, err := fmt.Fprintln(out, "already up to date")
		return err
	}

	if _, err := fmt.Fprintf(out, "synced onto %s\n  %d written, %d removed, %s\n",
		result.To, result.Written, result.Removed, humanBytes(result.Bytes)); err != nil {
		return err
	}
	if len(result.Conflicts) == 0 {
		return nil
	}

	if _, err := fmt.Fprintf(out, "\n%d conflict(s) - your version is in place, theirs is in %s:\n",
		len(result.Conflicts), filepath.Join(client.SpaceDir, "conflicts")); err != nil {
		return err
	}
	for _, c := range result.Conflicts {
		if _, err := fmt.Fprintf(out, "  %s (%s)\n", c.Path, c.Kind); err != nil {
			return err
		}
	}
	return nil
}
