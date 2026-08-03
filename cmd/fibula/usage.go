package main

// The command list, kept apart from the dispatch so that adding a command
// touches one table and one help text rather than a file that keeps growing.
const usage = `fibula - version control for binary assets

Usage:
  fibula <command> [arguments]

Commands:
  init <store>       create a space here, pointing at a store directory
  status             what changed since the last snapshot
  snapshot           record the working directory as an auto snapshot
  commit -m <msg>    record it as a deliberate version, which never expires
  log                deliberate history, newest first
  snapshots          the snapshot timeline
  promote <version>  turn a snapshot into a deliberate version
  checkout <target>  put the working directory into the state of a ref or version
  sync               bring the working directory onto the current state of the ref
  locking on|off     turn file locking on or off for this project
  lock <pattern>     reserve files, so others are refused at commit time
  unlock <pattern>   give them up again
  locks              who holds what
  diff <a> <b>       what changed between two states
  space check [--verbose]
                     report whether the space could be cleared safely
  space clear        delete asset files that are provably in the store
  restore            write the current version back into the working directory
  expire             apply the thinning schedule to the snapshot timeline
  gc [--dry-run]     delete store objects nothing reachable references
  version            print the client version
  help               print this message

Phase 1: unstable, no compatibility guarantees.
`

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

// lockedAdvice accompanies ErrLockHeld. A refusal without a way forward is
// half an answer, and the way forward here is a conversation - a lock is a
// coordination aid, not a permission system (E51).
const lockedAdvice = `
  fibula locks             see who holds it and since when
  fibula snapshot          keep your work while you sort it out

If they are unreachable, 'fibula lock <path> --force' takes it over and records
that you did. An expired lock needs no --force.
`
