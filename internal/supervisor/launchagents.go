package supervisor

type LaunchAgentOptions struct {
	RunMode          string // node|supervisor
	KeepAlive        bool
	ThrottleInterval int

	// supervisor mode
	DaemonBin string

	// env in plist
	CodexBin  string
	CodexHome string
	PathValue string
}
