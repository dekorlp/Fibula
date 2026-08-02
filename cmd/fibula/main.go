// Command fibula is the Fibula command line client.
//
// The CLI is thin orchestration only and carries no format logic (CLAUDE.md
// section Architecture and layering). At this point it is a skeleton: the
// command set is deliberately open (CLAUDE.md section Deliberately open), so
// only version and help exist.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// version is the client version. Phase 1 makes no compatibility promise, so
// nothing is released and the version stays a development marker.
const version = "0.0.0-dev"

const usage = `fibula - version control for binary assets

Usage:
  fibula <command> [arguments]

Commands:
  version    print the client version
  help       print this message

Phase 1: unstable, no compatibility guarantees.
`

// errUsage reports a command line the client does not understand. It is
// returned rather than printed so that main stays the only place that decides
// the exit code.
var errUsage = errors.New("unknown command")

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "fibula:", err)
		if errors.Is(err, errUsage) {
			fmt.Fprint(os.Stderr, "\n", usage)
		}
		os.Exit(1)
	}
}

// run executes one command and writes its output to out. It exists separately
// from main so that the command dispatch is testable without a process.
func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		_, err := fmt.Fprint(out, usage)
		return err
	}

	switch args[0] {
	case "version":
		_, err := fmt.Fprintf(out, "fibula %s\n", version)
		return err
	case "help", "-h", "--help":
		_, err := fmt.Fprint(out, usage)
		return err
	default:
		return fmt.Errorf("%w: %q", errUsage, args[0])
	}
}
