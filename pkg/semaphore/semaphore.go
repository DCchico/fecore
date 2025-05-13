package semaphore

/*
#cgo LDFLAGS: -pthread
#include <semaphore.h>
#include <errno.h>
#include <stdlib.h>
#include <fcntl.h>
#include <string.h>

// Define the constants we need
#define O_CREAT_VAL O_CREAT
#define O_EXCL_VAL O_EXCL
#define O_RDWR_VAL O_RDWR

// Helper function to get errno
int get_errno() {
    return errno;
}

// Declare sem_open with proper return type
sem_t* sem_open_wrapper(const char *name, int oflag, mode_t mode, unsigned int value) {
    return sem_open(name, oflag, mode, value);
}
*/
import "C"
import (
	"fmt"
	"unsafe"
)

const (
	O_CREAT = C.O_CREAT_VAL
	O_EXCL  = C.O_EXCL_VAL
	O_RDWR  = C.O_RDWR_VAL
)

type Semaphore struct {
	sem *C.sem_t
}

// Open opens a named semaphore with the specified flags and initial value.
// name: The name of the semaphore
// oflag: Open flags (O_CREAT, O_EXCL, O_RDWR)
// mode: File permissions if creating (e.g., 0666)
// value: Initial value if creating
func Open(name string, oflag int, mode uint32, value uint) (*Semaphore, error) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	sem := C.sem_open_wrapper(cname, C.int(oflag), C.mode_t(mode), C.uint(value))
	if sem == C.SEM_FAILED {
		return nil, fmt.Errorf("failed to open semaphore: %s", C.GoString(C.strerror(C.get_errno())))
	}

	return &Semaphore{sem: sem}, nil
}

func (s *Semaphore) Wait() error {
	if C.sem_wait(s.sem) == -1 {
		return fmt.Errorf("failed to wait on semaphore: %s", C.GoString(C.strerror(C.get_errno())))
	}
	return nil
}

func (s *Semaphore) Post() error {
	if C.sem_post(s.sem) == -1 {
		return fmt.Errorf("failed to post semaphore: %s", C.GoString(C.strerror(C.get_errno())))
	}
	return nil
}

func (s *Semaphore) Close() error {
	if C.sem_close(s.sem) == -1 {
		return fmt.Errorf("failed to close semaphore: %s", C.GoString(C.strerror(C.get_errno())))
	}
	return nil
}

func Unlink(name string) error {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	if C.sem_unlink(cname) == -1 {
		return fmt.Errorf("failed to unlink semaphore: %s", C.GoString(C.strerror(C.get_errno())))
	}
	return nil
}
