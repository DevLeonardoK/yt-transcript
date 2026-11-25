//go:build !linux && !darwin && !windows

package main

// physicalCores devolve 0 (desconhecido): defaultThreads usa runtime.NumCPU.
func physicalCores() int { return 0 }
