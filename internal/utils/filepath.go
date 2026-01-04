package utils

import (
	"path/filepath"
)

// Join is a simple wrapper around filepath.Join
func Join(elem ...string) string {
	return filepath.Join(elem...)
}
