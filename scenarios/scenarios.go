// Package scenarios holds what `rehearse bench` measures against. The
// recordings it plays are one JSON file each, in this folder. The labelled
// parts it sorts are one .diff file each, in the folder sorting. To add
// one of either, add a file.
package scenarios

import "embed"

// Files are the scenario files and the labelled parts built into the binary.
//
//go:embed *.json sorting/*.diff
var Files embed.FS
