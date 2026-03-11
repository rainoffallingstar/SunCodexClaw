//go:build !darwin

package supervisor

import "fmt"

func (s *Supervisor) InstallLaunchAgents(accounts []string, opts LaunchAgentOptions) ([]string, error) {
	return nil, fmt.Errorf("launchagents is only supported on macOS (darwin)")
}

func (s *Supervisor) UninstallLaunchAgents(accounts []string) ([]string, error) {
	return nil, fmt.Errorf("launchagents is only supported on macOS (darwin)")
}

func (s *Supervisor) StatusLaunchAgents(accounts []string, opts LaunchAgentOptions) ([]string, error) {
	return nil, fmt.Errorf("launchagents is only supported on macOS (darwin)")
}
