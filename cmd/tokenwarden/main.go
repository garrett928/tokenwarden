// Command tokenwarden is the CLI client for the tokenwardend daemon: queue
// management (add/list/show/cancel) and a health check, all over the
// daemon's HTTP API via internal/cliclient. It holds no state and no
// business logic of its own.
package main

import (
	"fmt"
	"os"

	"tokenwarden/internal/config"
)

var usage = `tokenwarden — CLI client for the tokenwarden daemon

Usage:
  tokenwarden status
  tokenwarden queue add --kind <kind> --prompt <text> [flags]
  tokenwarden queue list [--status <status>[,<status>...]]
  tokenwarden queue show <job-id>
  tokenwarden queue cancel <job-id>

Global:
  Set TOKENWARDEN_ADDR to point at a non-default daemon address
  (default: http://` + config.DefaultListenAddr + `)

Run 'tokenwarden queue add -h' (etc.) for flags on a specific command.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "tokenwarden:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}

	switch args[0] {
	case "status":
		return cmdStatus(args[1:])
	case "queue":
		return dispatchQueue(args[1:])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q — run 'tokenwarden help'", args[0])
	}
}

func dispatchQueue(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("queue requires a subcommand: add, list, show, cancel")
	}
	switch args[0] {
	case "add":
		return cmdQueueAdd(args[1:])
	case "list":
		return cmdQueueList(args[1:])
	case "show":
		return cmdQueueShow(args[1:])
	case "cancel":
		return cmdQueueCancel(args[1:])
	default:
		return fmt.Errorf("unknown queue subcommand %q", args[0])
	}
}
