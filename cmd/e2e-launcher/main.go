// Command e2e-launcher is the program the e2e monitor's launchd LaunchAgent runs
// (scripts/e2e-monitor-schedule.sh installs it outside the repo). The repo lives in iCloud Drive, which
// macOS privacy protection (TCC) closes to background jobs, so a LaunchAgent running /bin/bash directly
// gets "Operation not permitted". Granting Full Disk Access to this one binary, instead of to /bin/bash,
// lets the scheduled run read the repo; the script it starts inherits the grant.
//
//	e2e-launcher <path to scripts/e2e-monitor.sh>
//
// When the script can't be opened, it shows a macOS notification saying what to grant and exits 126.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: e2e-launcher <scripts/e2e-monitor.sh>")
		os.Exit(2)
	}
	script := os.Args[1]
	f, err := os.Open(script)
	if err != nil {
		self, _ := os.Executable()
		msg := fmt.Sprintf("cannot read %s: %v. Grant Full Disk Access to %s (System Settings > Privacy & Security).", script, err, self)
		fmt.Fprintln(os.Stderr, "e2e-launcher:", msg)
		_ = exec.Command("/usr/bin/osascript", "-e", "on run argv", "-e", "display notification (item 1 of argv) with title \"Sockabus e2e monitor blocked\"", "-e", "end run", msg).Run()
		os.Exit(126)
	}
	_ = f.Close()

	cmd := exec.Command("/bin/bash", script)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, os.Stdout, os.Stderr
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		os.Exit(exit.ExitCode())
	default:
		fmt.Fprintln(os.Stderr, "e2e-launcher:", err)
		os.Exit(1)
	}
}
