package skillmanager

import "golang.org/x/sys/unix"

func exchange(fromFD int, from string, toFD int, to string, exists bool) error {
	flag := uint32(unix.RENAME_EXCL)
	if exists {
		flag = unix.RENAME_SWAP
	}
	return unix.RenameatxNp(fromFD, from, toFD, to, flag)
}
