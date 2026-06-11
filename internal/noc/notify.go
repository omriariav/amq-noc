package noc

import (
	"os/exec"
	"strconv"
)

// DesktopNotify posts a desktop notification (macOS osascript, best effort:
// failures are silent because a notification is advisory, never load-bearing).
// It is the production seam behind the NOC's opt-in needs-you notifier (#28);
// tests inject a recorder instead.
func DesktopNotify(title, body string) {
	script := "display notification " + strconv.Quote(body) + " with title " + strconv.Quote(title)
	_ = notifyExec("osascript", "-e", script)
}

// notifyExec is the injectable subprocess seam for DesktopNotify.
var notifyExec = func(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}
