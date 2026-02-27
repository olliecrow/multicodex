package main

// ExitError represents a controlled command exit code.
type ExitError struct {
	Code    int
	Message string
}

func (e *ExitError) Error() string {
	if e.Message == "" {
		return "command failed"
	}
	return e.Message
}
