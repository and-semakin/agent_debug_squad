package skillmanager

import "golang.org/x/sys/unix"

func exchange(fromFD int, from string, toFD int, to string, exists bool) error {
	flag := uint(unix.RENAME_NOREPLACE)
	if exists {
		flag = unix.RENAME_EXCHANGE
	}
	return unix.Renameat2(fromFD, from, toFD, to, flag)
}
