package main

import (
	"os"
	"path/filepath"
	"strings"
)

// physicalCores conta os núcleos distintos pelo sysfs (cada núcleo lista os
// seus hyperthreads em core_cpus_list).
func physicalCores() int {
	files, _ := filepath.Glob("/sys/devices/system/cpu/cpu[0-9]*/topology/core_cpus_list")
	if len(files) == 0 {
		files, _ = filepath.Glob("/sys/devices/system/cpu/cpu[0-9]*/topology/thread_siblings_list")
	}
	cores := make(map[string]struct{}, len(files))
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil {
			cores[strings.TrimSpace(string(b))] = struct{}{}
		}
	}
	return len(cores)
}
