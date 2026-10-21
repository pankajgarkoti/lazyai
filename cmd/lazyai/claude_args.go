package main

import "strings"

// claudeArgs resumes this backend's saved conversation. An explicitly supplied
// native resume/continue/session flag takes precedence over automatic selection.
func claudeArgs(base []string, sessionID string) []string {
	args := append([]string(nil), base...)
	if sessionID == "" {
		return args
	}
	for _, arg := range args {
		switch {
		case arg == "-r", arg == "-c", arg == "--resume", arg == "--continue", arg == "--session-id",
			strings.HasPrefix(arg, "--resume="), strings.HasPrefix(arg, "--session-id="):
			return args
		}
	}
	return append([]string{"--resume", sessionID}, args...)
}
