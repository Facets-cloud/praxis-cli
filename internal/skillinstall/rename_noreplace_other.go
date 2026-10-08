//go:build !darwin && !linux && !windows

package skillinstall

import "fmt"

func renameNoReplace(from, to string) error {
	return fmt.Errorf("exclusive skill activation is unsupported on this platform")
}
