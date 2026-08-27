package command

import "regexp"

// maxFailureMarkers bounds how many failing lines are surfaced. The point is to
// contradict a zero exit code, not to reproduce the log.
const maxFailureMarkers = 5

// failureMarkerPatterns match lines that a toolchain only prints when something
// failed.
//
// They are deliberately anchored and few. A pattern that fires on ordinary
// output would teach the caller to ignore the field, which is worse than not
// having it.
var failureMarkerPatterns = []*regexp.Regexp{
	// go test, per test and per package
	regexp.MustCompile(`^--- FAIL: `),
	regexp.MustCompile(`^FAIL\s`),
	// go build, go vet and golangci-lint all report file:line:col
	regexp.MustCompile(`^\S+\.go:\d+:\d+: `),
	// lean
	regexp.MustCompile(`^\S+\.lean:\d+:\d+: error: `),
	// cargo and rustc
	regexp.MustCompile(`^error(\[E\d+\])?: `),
	// pytest summary
	regexp.MustCompile(`^FAILED \S`),
}

// failureMarkers returns lines that say the command failed.
//
// A command exits with the status of the last stage of its pipeline, so
// `go test ./... | tail -40` reports success whatever the tests did. In the
// durable log this masked a real failure twenty-three times. The exit code
// cannot be repaired from here, but the caller can be told not to trust it.
func failureMarkers(lines []string) []string {
	var found []string
	for _, line := range lines {
		for _, pattern := range failureMarkerPatterns {
			if !pattern.MatchString(line) {
				continue
			}
			found = append(found, line)
			break
		}
		if len(found) == maxFailureMarkers {
			break
		}
	}
	return found
}

// maskedFailureMarkers returns failure markers only when the exit code claims
// success, which is the only case where the caller learns something new.
func maskedFailureMarkers(exitCode int, lines []string) []string {
	if exitCode != 0 {
		return nil
	}
	return failureMarkers(lines)
}
