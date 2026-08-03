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
