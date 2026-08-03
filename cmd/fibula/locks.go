package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/dekorlp/fibula/client"
	"github.com/dekorlp/fibula/store"
)

// runLocking switches locking on or off for the project and sets the expiry.
// The setting lives in the store, so it applies to everyone (E51).
func runLocking(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: locking on|off [--expiry <duration>]", errUsage)
	}

	space, _, err := openHere()
	if err != nil {
		return err
	}
	settings, err := space.Settings(ctx)
	if err != nil {
		return err
	}

	switch args[0] {
	case "on":
		settings.Locking = true
	case "off":
		settings.Locking = false
	case "status":
		return printLocking(out, settings)
	default:
		return fmt.Errorf("%w: locking on|off [--expiry <duration>]", errUsage)
	}

	if len(args) == 3 && args[1] == "--expiry" {
		d, err := time.ParseDuration(args[2])
		if err != nil {
			return fmt.Errorf("--expiry: %w", err)
		}
		settings.LockExpiry = d
	} else if len(args) != 1 {
		return fmt.Errorf("%w: locking on|off [--expiry <duration>]", errUsage)
	}

	if err := space.SetSettings(ctx, settings); err != nil {
		return err
	}
	return printLocking(out, settings)
}

func printLocking(out io.Writer, settings store.Settings) error {
	state := "off"
	if settings.Locking {
		state = "on"
	}
	_, err := fmt.Fprintf(out, "locking %s, locks expire after %s\n", state, settings.Expiry())
	return err
}

func runLock(ctx context.Context, args []string, out io.Writer) error {
	pattern, reason, force, err := parseLockArgs(args, "lock <path|pattern> [-m \"reason\"] [--force]")
	if err != nil {
		return err
	}

	space, ignore, err := openHere()
	if err != nil {
		return err
	}
	owner, err := currentAuthor()
	if err != nil {
		return err
	}

	taken, err := space.Lock(ctx, ignore, client.LockRequest{
		Pattern: pattern, Owner: owner, Reason: reason, Now: time.Now(), Force: force,
	})
	// Whatever was taken before an error is reported anyway: a half-finished
	// run the user cannot see is how files end up reserved by accident.
	for _, lock := range taken {
		verb := "locked"
		if lock.BrokenFrom != "" {
			verb = fmt.Sprintf("taken from %s", lock.BrokenFrom)
		}
		if _, printErr := fmt.Fprintf(out, "%s  %s\n", verb, lock.Path); printErr != nil {
			return printErr
		}
	}
	return err
}

func runUnlock(ctx context.Context, args []string, out io.Writer) error {
	pattern, _, force, err := parseLockArgs(args, "unlock <path|pattern> [--force]")
	if err != nil {
		return err
	}

	space, ignore, err := openHere()
	if err != nil {
		return err
	}
	owner, err := currentAuthor()
	if err != nil {
		return err
	}

	released, err := space.Unlock(ctx, ignore, pattern, owner, force)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "released %d lock(s)\n", released)
	return err
}

func runLocks(ctx context.Context, out io.Writer) error {
	space, _, err := openHere()
	if err != nil {
		return err
	}
	locks, err := space.Locks(ctx)
	if err != nil {
		return err
	}
	if len(locks) == 0 {
		_, err := fmt.Fprintln(out, "no locks")
		return err
	}

	for _, lock := range locks {
		line := fmt.Sprintf("%s\n  held by %s since %s",
			lock.Path, lock.Owner, lock.Since.UTC().Format(time.RFC3339))
		if lock.Reason != "" {
			line += fmt.Sprintf("\n  reason: %s", lock.Reason)
		}
		if lock.BrokenFrom != "" {
			line += fmt.Sprintf("\n  taken from %s on %s",
				lock.BrokenFrom, lock.BrokenAt.UTC().Format(time.RFC3339))
		}
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}
	return nil
}

// parseLockArgs reads the shared shape of lock and unlock. An unrecognized flag
// is an error rather than being ignored, so a typo cannot silently become a
// different operation.
func parseLockArgs(args []string, usageLine string) (pattern, reason string, force bool, err error) {
	if len(args) == 0 {
		return "", "", false, fmt.Errorf("%w: %s", errUsage, usageLine)
	}
	pattern = args[0]

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--force":
			force = true
		case "-m":
			if i+1 >= len(args) {
				return "", "", false, fmt.Errorf("%w: -m needs a reason", errUsage)
			}
			i++
			reason = args[i]
		default:
			return "", "", false, fmt.Errorf("%w: %s", errUsage, usageLine)
		}
	}
	return pattern, reason, force, nil
}
