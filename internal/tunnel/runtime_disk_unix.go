//go:build linux || darwin

package tunnel

import "syscall"

func runtimeFreeSpace(path string) (uint64, error) {
	var st syscall.Statfs_t
	err := syscall.Statfs(path, &st)
	return uint64(st.Bavail) * uint64(st.Bsize), err
}
