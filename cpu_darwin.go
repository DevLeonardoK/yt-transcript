package main

import "syscall"

// physicalCores lê hw.physicalcpu. Apple Silicon não tem hyperthreading, mas
// Macs Intel têm.
func physicalCores() int {
	n, err := syscall.SysctlUint32("hw.physicalcpu")
	if err != nil {
		return 0
	}
	return int(n)
}
