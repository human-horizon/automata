package scrollback

import "fmt"

// DefaultLines is the scrollback budget used when the caller does not configure one.
const DefaultLines = 300

// Validate rejects limits below zero; zero means unbounded history.
func Validate(lines int) error {
	if lines < 0 {
		return fmt.Errorf("scrollback line limit must be zero or greater: %d", lines)
	}
	return nil
}
