// Commands for the version graph: deliberate versions, history, promotion,
// checkout, diff, expiry and garbage collection.
package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/dekorlp/fibula/client"
	"github.com/dekorlp/fibula/store/fs"
)

func runCommit(ctx context.Context, args []string, out io.Writer) error {
	if len(args) != 2 || args[0] != "-m" {
		return fmt.Errorf("%w: commit needs -m \"message\"", errUsage)
	}

	space, ignore, err := openHere()
	if err != nil {
		return err
	}
	author, err := currentAuthor()
	if err != nil {
		return err
	}

	result, err := space.Commit(ctx, ignore, client.SnapshotOptions{
		Author: author, Message: args[1], Now: time.Now(),
	})
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(out, "version %s\n  %d files, %d read and chunked, %s written\n",
		result.Version, result.Files, result.Hashed, humanBytes(result.Uploaded))
	return err
}

func runLog(ctx context.Context, out io.Writer) error {
	space, _, err := openHere()
	if err != nil {
		return err
	}
	head, err := space.Head()
	if err != nil {
		return err
	}

	entries, err := space.Log(ctx, head.Ref.Name(), 0)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if _, err := fmt.Fprintf(out, "%s  %s  %s\n    %s\n",
			e.Version.String()[:12], e.Time.Format(time.RFC3339), e.Author, e.Message); err != nil {
			return err
		}
	}
	return nil
}

func runSnapshots(ctx context.Context, out io.Writer) error {
	space, _, err := openHere()
	if err != nil {
		return err
	}

	timeline, err := space.Timeline(ctx)
	if err != nil {
		return err
	}
	for _, s := range timeline {
		if _, err := fmt.Fprintf(out, "%s  %s  expires %s\n",
			s.Version.String()[:12], s.Time.Format(time.RFC3339), s.Expiry.Format(time.RFC3339)); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(out, "%d snapshots\n", len(timeline))
	return err
}

func runPromote(ctx context.Context, args []string, out io.Writer) error {
	if len(args) != 3 || args[1] != "-m" {
		return fmt.Errorf("%w: promote <version> -m \"message\"", errUsage)
	}

	space, _, err := openHere()
	if err != nil {
		return err
	}
	id, err := space.Resolve(ctx, args[0])
	if err != nil {
		return err
	}
	author, err := currentAuthor()
	if err != nil {
		return err
	}

	result, err := space.Promote(ctx, id, client.SnapshotOptions{
		Author: author, Message: args[2], Now: time.Now(),
	})
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(out, "promoted %s to version %s\n", args[0], result.Version)
	return err
}

func runCheckout(ctx context.Context, args []string, out io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: checkout needs a ref or a version", errUsage)
	}

	space, ignore, err := openHere()
	if err != nil {
		return err
	}
	author, err := currentAuthor()
	if err != nil {
		return err
	}

	result, err := space.Checkout(ctx, ignore, args[0], client.ClearOptions{Author: author, Now: time.Now()})
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(out, "checked out %s\n  %d files written, %d removed, %s\n",
		result.Version, result.Written, result.Removed, humanBytes(result.Bytes))
	return err
}

func runDiff(ctx context.Context, args []string, out io.Writer) error {
	if len(args) != 2 {
		return fmt.Errorf("%w: diff needs two states", errUsage)
	}

	space, _, err := openHere()
	if err != nil {
		return err
	}
	changes, err := space.Diff(ctx, args[0], args[1])
	if err != nil {
		return err
	}

	if changes.IsEmpty() {
		_, err := fmt.Fprintln(out, "no changes")
		return err
	}
	for _, e := range changes.Added {
		fmt.Fprintf(out, "+ %s\n", e.Path) //nolint:errcheck // reported by the final write
	}
	for _, e := range changes.Removed {
		fmt.Fprintf(out, "- %s\n", e.Path) //nolint:errcheck // see above
	}
	for _, c := range changes.Changed {
		fmt.Fprintf(out, "M %s\n", c.Path()) //nolint:errcheck // see above
	}
	for _, r := range changes.Renamed {
		fmt.Fprintf(out, "R %s -> %s\n", r.Before.Path, r.After.Path) //nolint:errcheck // see above
	}
	_, err = fmt.Fprintf(out, "%d added, %d removed, %d changed, %d renamed\n",
		len(changes.Added), len(changes.Removed), len(changes.Changed), len(changes.Renamed))
	return err
}

func runExpire(ctx context.Context, out io.Writer) error {
	space, _, err := openHere()
	if err != nil {
		return err
	}

	result, err := space.Expire(ctx, client.DefaultRetention(), time.Now())
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(out, "%d snapshots kept, %d expired\n"+
		"expiry removes timeline entries only; run `fibula gc` to reclaim space\n",
		result.Kept, len(result.Expired))
	return err
}

func runGC(ctx context.Context, args []string, out io.Writer) error {
	space, _, err := openHere()
	if err != nil {
		return err
	}

	opts := client.GCOptions{Now: time.Now()}
	for _, arg := range args {
		if arg != "--dry-run" {
			return fmt.Errorf("%w: gc %q", errUsage, arg)
		}
		opts.DryRun = true
	}

	objects, err := fs.OpenGC(space.Config().Store)
	if err != nil {
		return err
	}

	result, err := client.Collect(ctx, objects, space.Refs(), space.Config().Store, opts)
	if err != nil {
		return err
	}

	verb := "deleted"
	if opts.DryRun {
		verb = "would delete"
	}
	_, err = fmt.Fprintf(out, "%d objects reachable, %d scanned, %s %d (%s), %d spared by the grace window\n",
		result.Reachable, result.Scanned, verb, result.Deleted, humanBytes(result.Bytes), result.Spared)
	return err
}
