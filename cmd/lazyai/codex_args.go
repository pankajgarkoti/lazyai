package main

// codexArgs resumes this backend's saved conversation. An explicitly supplied
// native resume/fork command takes precedence over automatic selection.
func codexArgs(base []string, sessionID string) []string {
	args := append([]string(nil), base...)
	if sessionID == "" {
		return args
	}
	for _, arg := range args {
		if arg == "resume" || arg == "fork" {
			return args
		}
	}
	return append([]string{"resume", sessionID}, args...)
}
