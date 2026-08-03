// Command fibula is the Fibula command line client.
//
// The CLI is thin orchestration only and carries no format logic (CLAUDE.md
// section Architecture and layering). Command names are explicitly open
// (CLAUDE.md section Deliberately open), so these are a starting point rather
// than a promise.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"time"

	"github.com/dekorlp/fibula/client"
	"github.com/dekorlp/fibula/errs"
)

// version is the client version. Phase 1 makes no compatibility promise, so
// nothing is released and the version stays a development marker.
const version = "0.0.0-dev"

// errUsage reports a command line the client does not understand.
var errUsage = errors.New("unknown command")

// behindAdvice is printed alongside ErrSpaceBehind. Refusing the commit is only
// half an answer; sync is the other half (E52).
//
// The snapshot line stays as the cautious route: it is safe precisely because
// snapshots land on their own ref and cannot collide (E12, TP-005 TC-409), so
// the current directory can be preserved before anything touches it.
const behindAdvice = `
  fibula sync              bring your directory onto the current state
  fibula snapshot          keep the current directory first, if unsure

Files both sides changed are left for you to decide; everything else merges
on its own.
`

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "fibula:", err)
		switch {
		case errors.Is(err, errUsage):
			fmt.Fprint(os.Stderr, "\n", usage)
		case errors.Is(err, errs.ErrSpaceBehind):
			fmt.Fprint(os.Stderr, behindAdvice)
		}
		os.Exit(1)
	}
}

// run executes one command. It exists separately from main so that the command
// dispatch is testable without a process.
func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		_, err := fmt.Fprint(out, usage)
		return err
	}

	if handler, ok := commands[args[0]]; ok {
		return handler(ctx, args[1:], out)
	}
	return fmt.Errorf("%w: %q", errUsage, args[0])
}

// handler is one command. Every command takes the same shape so that dispatch
// is a table rather than a switch that grows a case per feature.
type handler func(ctx context.Context, args []string, out io.Writer) error

var commands = map[string]handler{
	"version":   func(_ context.Context, _ []string, out io.Writer) error { return printVersion(out) },
	"help":      func(_ context.Context, _ []string, out io.Writer) error { return printUsage(out) },
	"-h":        func(_ context.Context, _ []string, out io.Writer) error { return printUsage(out) },
	"--help":    func(_ context.Context, _ []string, out io.Writer) error { return printUsage(out) },
	"init":      func(_ context.Context, args []string, out io.Writer) error { return runInit(args, out) },
	"status":    func(ctx context.Context, _ []string, out io.Writer) error { return runStatus(ctx, out) },
	"snapshot":  func(ctx context.Context, _ []string, out io.Writer) error { return runSnapshot(ctx, out) },
	"commit":    runCommit,
	"log":       func(ctx context.Context, _ []string, out io.Writer) error { return runLog(ctx, out) },
	"snapshots": func(ctx context.Context, _ []string, out io.Writer) error { return runSnapshots(ctx, out) },
	"promote":   runPromote,
	"checkout":  runCheckout,
	"diff":      runDiff,
	"expire":    func(ctx context.Context, _ []string, out io.Writer) error { return runExpire(ctx, out) },
	"gc":        runGC,
	"space":     runSpace,
	"restore":   func(ctx context.Context, _ []string, out io.Writer) error { return runRestore(ctx, out) },
	"sync":      func(ctx context.Context, _ []string, out io.Writer) error { return runSync(ctx, out) },
	"lock":      runLock,
	"unlock":    runUnlock,
	"locks":     func(ctx context.Context, _ []string, out io.Writer) error { return runLocks(ctx, out) },
	"locking":   runLocking,
}

func printVersion(out io.Writer) error {
	_, err := fmt.Fprintf(out, "fibula %s\n", version)
	return err
}

func printUsage(out io.Writer) error {
	_, err := fmt.Fprint(out, usage)
	return err
}

func runInit(args []string, out io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: init needs exactly one store directory", errUsage)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("current directory: %w", err)
	}
	space, err := client.Init(cwd, args[0])
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(out, "space created in %s, store %s\n", space.Root(), space.Config().Store)
	return err
}

func runStatus(ctx context.Context, out io.Writer) error {
	space, ignore, err := openHere()
	if err != nil {
		return err
	}

	status, err := space.Status(ctx, ignore)
	if err != nil {
		return err
	}

	if status.IsClean() {
		if _, err := fmt.Fprintf(out, "clean, %d files unchanged\n", status.Unchanged); err != nil {
			return err
		}
		// Still reported: a clean directory can hold an unfinished decision.
		return printPendingConflicts(space, out)
	}

	printList(out, "added", status.Added)
	printList(out, "modified", status.Modified)
	printList(out, "removed", status.Removed)
	if _, err = fmt.Fprintf(out, "%d unchanged\n", status.Unchanged); err != nil {
		return err
	}
	return printPendingConflicts(space, out)
}

func runSnapshot(ctx context.Context, out io.Writer) error {
	space, ignore, err := openHere()
	if err != nil {
		return err
	}

	author, err := currentAuthor()
	if err != nil {
		return err
	}

	now := time.Now()
	result, err := space.Snapshot(ctx, ignore, client.SnapshotOptions{
		Author: author,
		Expiry: now.AddDate(0, 6, 0),
		Now:    now,
	})
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(out, "snapshot %s\n  %d files, %d read, %s chunked\n",
		result.Version, result.Files, result.Hashed, humanBytes(result.Chunked)); err != nil {
		return err
	}
	return reportBudget(ctx, space, ignore, out)
}

// reportBudget suggests clearing when the budget is reached. It never clears:
// the space manager suggests, the user decides (E15).
func reportBudget(ctx context.Context, space *client.Space, ignore *client.Ignore, out io.Writer) error {
	used, reached, err := space.Budget(ctx, ignore)
	if err != nil || !reached {
		return err
	}

	_, err = fmt.Fprintf(out,
		"\nthe working directory holds %s, at or above the configured budget of %s\n"+
			"everything is snapshotted; `fibula space clear` frees the local copy\n",
		humanBytes(used), humanBytes(space.Config().Budget))
	return err
}

func runSpace(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: space needs a subcommand, check or clear", errUsage)
	}

	switch args[0] {
	case "check":
		return runSpaceCheck(ctx, args[1:], out)
	case "clear":
		return runSpaceClear(ctx, args[1:], out)
	default:
		return fmt.Errorf("%w: space %q", errUsage, args[0])
	}
}

func runSpaceCheck(ctx context.Context, args []string, out io.Writer) error {
	verbose := false
	for _, arg := range args {
		if arg != "--verbose" {
			return fmt.Errorf("%w: space check %q", errUsage, arg)
		}
		verbose = true
	}

	space, ignore, err := openHere()
	if err != nil {
		return err
	}

	check, err := space.CheckClear(ctx, ignore)
	if err != nil {
		return err
	}

	// The safe list is one line per asset — 3,050 on the scale run and one per
	// file on a real project (F-B-02). It is also the least interesting of the
	// three: it is the expected case. Summarizing it is what keeps the two
	// lists that decide whether data is at risk visible at all.
	if verbose {
		printList(out, "safe to delete", check.Safe)
	} else if len(check.Safe) > 0 {
		if _, err := fmt.Fprintf(out, "safe to delete: %d files\n", len(check.Safe)); err != nil {
			return err
		}
	}

	// Unversioned is never summarized, however long it gets. These files are
	// about to be snapshotted and then deleted, and which ones they are is
	// exactly what a user needs to see before agreeing to that.
	printList(out, "not versioned yet, would be snapshotted first", check.Unversioned)
	if len(check.Ignored) > 0 {
		if _, err := fmt.Fprintf(out, "%s of ignored files would stay in place\n",
			humanBytes(check.IgnoredBytes)); err != nil {
			return err
		}
	}
	if !check.OK() {
		return check.Err()
	}

	_, err = fmt.Fprintln(out, "the space can be cleared")
	return err
}

func runSpaceClear(ctx context.Context, args []string, out io.Writer) error {
	space, ignore, err := openHere()
	if err != nil {
		return err
	}

	opts := client.ClearOptions{Now: time.Now()}
	for _, arg := range args {
		if arg != "--include-ignored" {
			return fmt.Errorf("%w: space clear %q", errUsage, arg)
		}
		opts.IncludeIgnored = true
	}
	if opts.Author, err = currentAuthor(); err != nil {
		return err
	}

	result, err := space.Clear(ctx, ignore, opts)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(out, "deleted %d files, %s freed\n",
		result.Deleted, humanBytes(result.DeletedBytes)); err != nil {
		return err
	}
	if result.Snapshotted > 0 {
		if _, err := fmt.Fprintf(out, "%d files were not versioned and were snapshotted first\n",
			result.Snapshotted); err != nil {
			return err
		}
	}
	if result.Ignored > 0 {
		if _, err := fmt.Fprintf(out,
			"%s of ignored files not deleted, --include-ignored to include them\n",
			humanBytes(result.IgnoredBytes)); err != nil {
			return err
		}
	}
	return nil
}

func runRestore(ctx context.Context, out io.Writer) error {
	space, _, err := openHere()
	if err != nil {
		return err
	}

	result, err := space.Restore(ctx)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(out, "restored %d files, %s\n", result.Files, humanBytes(result.Bytes))
	return err
}

func openHere() (*client.Space, *client.Ignore, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, fmt.Errorf("current directory: %w", err)
	}

	space, err := client.Open(cwd)
	if err != nil {
		return nil, nil, err
	}
	ignore, err := client.LoadIgnore(space.Root())
	if err != nil {
		return nil, nil, err
	}
	return space, ignore, nil
}

// currentAuthor is the account the version is recorded under. The server
// validates it on push and rejects a mismatch rather than correcting it (E30),
// so getting it from the operating system is a starting point and not an
// identity system.
func currentAuthor() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("determine the current user: %w", err)
	}
	if u.Username == "" {
		return "", errors.New("the current user has no name")
	}
	return u.Username, nil
}

func printList(out io.Writer, label string, paths []string) {
	if len(paths) == 0 {
		return
	}
	fmt.Fprintf(out, "%s (%d):\n", label, len(paths)) //nolint:errcheck // reported by the caller's final write
	for _, p := range paths {
		fmt.Fprintf(out, "  %s\n", p) //nolint:errcheck // see above
	}
}

// humanBytes renders a size the way a person reads it. Binary units, because
// that is what the chunk parameters and the budget are expressed in.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}

	div, exp := int64(unit), 0
	for size := n / unit; size >= unit && exp < 4; size /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}
