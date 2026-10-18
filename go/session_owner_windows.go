package main

import "os"

// Local viewer control currently requires the Unix runtime-directory contract.
func sessionOwned(info os.FileInfo) bool { return false }
