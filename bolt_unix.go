//go:build !windows && !plan9 && !solaris && !aix && !android

package bbolt

import (
	//"fmt"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/glycerine/bbolt/errors"
	"github.com/glycerine/bbolt/internal/common"
)

// flock acquires an advisory lock on a file descriptor.
func flock(db *DB, exclusive bool, timeout time.Duration) error {
	var t time.Time
	if timeout != 0 {
		t = time.Now()
	}
	fd := db.file.Fd()
	flag := syscall.LOCK_NB
	if exclusive {
		flag |= syscall.LOCK_EX
	} else {
		flag |= syscall.LOCK_SH
	}
	for {
		// Attempt to obtain an exclusive lock.
		err := syscall.Flock(int(fd), flag)
		if err == nil {
			return nil
		} else if err != syscall.EWOULDBLOCK {
			return err
		}

		// If we timed out then return an error.
		if timeout != 0 && time.Since(t) > timeout-flockRetryTimeout {
			return errors.ErrTimeout
		}

		// Wait for a bit and try again.
		time.Sleep(flockRetryTimeout)
	}
}

// funlock releases an advisory lock on a file descriptor.
func funlock(db *DB) error {
	return syscall.Flock(int(db.file.Fd()), syscall.LOCK_UN)
}

// mmap memory maps a DB's data file.
func mmap(db *DB, sz int) error {
	// Map the data file to memory.
	b, err := unix.Mmap(int(db.file.Fd()), 0, sz, syscall.PROT_READ, syscall.MAP_SHARED|db.MmapFlags)
	if err != nil {
		return err
	}

	// per https://github.com/etcd-io/bbolt/pull/940
	// and
	// https://github.com/etcd-io/bbolt/issues/939
	// MADV_RANDOM is best omitted:
	//
	// "In addition to the explicit documented behavior in posix_madvise(2) this
	// call since Linux 6.4 also causes the kernel to aggressively free pages
	// from the page cache by short circuiting the LRU second chance mechanism.
	// The result is compaction events that took 900ms now take up to 20s and
	// a system which generally operated with near zero major page faults sees
	// 600 or more major faults per second during compaction events.
	//
	// "We've tested this change in older kernels and observed no negative impact
	// in typical cloud instances."
	//
	// Fixes #939
	//
	// -- https://github.com/sdodson
	//
	// Advise the kernel that the mmap is accessed randomly.
	// err = unix.Madvise(b, syscall.MADV_RANDOM)
	// if err != nil && err != syscall.ENOSYS {
	// 	// Ignore not implemented error in kernel because it still works.
	// 	return fmt.Errorf("madvise: %s", err)
	// }

	// Save the original byte slice and convert to a byte array pointer.
	db.dataref = b
	db.data = (*[common.MaxMapSize]byte)(unsafe.Pointer(&b[0]))
	db.datasz = sz
	return nil
}

// munmap unmaps a DB's data file from memory.
func munmap(db *DB) error {
	// Ignore the unmap if we have no mapped data.
	if db.dataref == nil {
		return nil
	}

	// Unmap using the original byte slice.
	err := unix.Munmap(db.dataref)
	db.dataref = nil
	db.data = nil
	db.datasz = 0
	return err
}
