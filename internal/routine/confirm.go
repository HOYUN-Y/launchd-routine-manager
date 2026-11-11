package routine

// Need names a confirmation a policy function wants before it will go on. The CLI answers with
// flags, the TUI with a dialog; either way the policy stays here.
type Need string

const (
	NeedLoad             Need = "load"              // job is not loaded: bootstrap it first?
	NeedRestart          Need = "restart"           // job is running: restart it?
	NeedRestartKeepAlive Need = "restart-keepalive" // running KeepAlive service: restart it?
	NeedReloadRunning    Need = "reload-running"    // schedule change reloads a running job
	NeedForce            Need = "force"             // current schedule cannot be expressed by rtm
)

// ConfirmError is returned instead of acting. Msg is the CLI wording (it names the flag).
type ConfirmError struct {
	Need Need
	Msg  string
}

func (c *ConfirmError) Error() string { return c.Msg }

// GrantRun records the user's yes in the options for Run.
func (c *ConfirmError) GrantRun(o *RunOpts) {
	switch c.Need {
	case NeedLoad:
		o.Load = true
	case NeedRestart:
		o.Restart = true
	case NeedRestartKeepAlive:
		o.Restart, o.Yes = true, true
	}
}

// GrantSched records the user's yes in the options for SetSchedule/RevertSchedule.
func (c *ConfirmError) GrantSched(o *SchedOpts) {
	switch c.Need {
	case NeedReloadRunning:
		o.Yes = true
	case NeedForce:
		o.Force = true
	}
}
