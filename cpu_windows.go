package main

import (
	"syscall"
	"unsafe"
)

var procGetLogicalProcessorInformation = syscall.NewLazyDLL("kernel32.dll").NewProc("GetLogicalProcessorInformation")

// systemLogicalProcessorInformation espelha SYSTEM_LOGICAL_PROCESSOR_INFORMATION.
// A união de 16 bytes é alinhada como ULONGLONG, igual a [2]uint64 em Go
// (32 bytes em amd64/arm64, 24 em 386).
type systemLogicalProcessorInformation struct {
	processorMask uintptr
	relationship  uint32
	_             [2]uint64
}

const relationProcessorCore = 0

// physicalCores conta as entradas RelationProcessorCore (uma por núcleo físico).
func physicalCores() int {
	var size uint32
	procGetLogicalProcessorInformation.Call(0, uintptr(unsafe.Pointer(&size)))
	if size == 0 {
		return 0
	}
	elem := uint32(unsafe.Sizeof(systemLogicalProcessorInformation{}))
	buf := make([]systemLogicalProcessorInformation, size/elem+1)
	size = uint32(len(buf)) * elem
	r, _, _ := procGetLogicalProcessorInformation.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == 0 {
		return 0
	}
	cores := 0
	for _, info := range buf[:size/elem] {
		if info.relationship == relationProcessorCore {
			cores++
		}
	}
	return cores
}
