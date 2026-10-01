//go:build !darwin && !linux

package skillinstall

import "fmt"

func renameNoReplace(from, to string) error {
	return fmt.Errorf("exclusive skill activation is unsupported on this platform")
}
