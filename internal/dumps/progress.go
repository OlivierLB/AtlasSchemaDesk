package dumps

import (
	"fmt"
	"os"
	"time"
)

// Progress records travel through the existing supervised output pipe. The
// supervisor retains the last phase even after verbose command output rotates.
func progress(phase string, current, total int64) {
	fmt.Fprintf(os.Stderr, "[sulcata-progress] %s %d %d\n", phase, current, total)
}

func watchDownload(path string, total int64) func() {
	done, stopped := make(chan struct{}), make(chan struct{})
	progress("downloading", 0, total)
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if info, err := os.Stat(path); err == nil {
					progress("downloading", min(info.Size(), total), total)
				}
			}
		}
	}()
	return func() { close(done); <-stopped }
}
