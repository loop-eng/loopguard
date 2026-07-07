package enforcer

import (
	"os"
	"path/filepath"
)

const sentinelFileName = ".loopguard-stop"

func writeSentinel(projectDir string) error {
	path := filepath.Join(projectDir, sentinelFileName)
	return os.WriteFile(path, []byte("paused by loopguard\n"), 0644)
}

func removeSentinel(projectDir string) error {
	path := filepath.Join(projectDir, sentinelFileName)
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
