// Package scenarios holds the recordings `rehearse bench` plays: one JSON
// file each, in this folder. To add one, add a file.
package scenarios

import "embed"

// Files are the scenario files built into the binary.
//
//go:embed *.json
var Files embed.FS
