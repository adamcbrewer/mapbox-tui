package main

import "os"

const sessionControlSupported = false
const regularFileOpenFlags = 0

// Local viewer control currently requires the Unix runtime-directory contract.
func sessionOwned(info os.FileInfo) bool { return false }
