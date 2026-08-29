package logging

import "path/filepath"

// ReconfigureValues is the config-package-neutral runtime reconfiguration seam
// used by the v2 application. The legacy Reconfigure method remains available
// until Phase 12 removes the v1 config topology.
func (l *Logger) ReconfigureValues(capture string, memoryMB int, writeDisk bool, diskMin string, maxFileMB, keep int) error {
	var next *diskSink
	var err error
	if writeDisk {
		next, err = newDiskSink(filepath.Join(l.root, "logs", "manager"), parseLevel(diskMin), maxFileMB, keep)
		if err != nil {
			return err
		}
	}
	l.mu.Lock()
	old := l.disk
	l.disk = next
	l.capture = effectiveCaptureLevel(capture, memoryMB, diskMin, maxFileMB, keep)
	l.ring.SetMaxMB(memoryMB)
	l.mu.Unlock()
	if old != nil {
		return old.close()
	}
	return nil
}
