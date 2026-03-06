// Package validate provides output format validation for worker responses,
// ensuring ===FILE: path=== ... ===ENDFILE=== blocks are well-formed.
package validate

import (
	"fmt"
	"strings"
)

const (
	filePrefix = "===FILE:"
	fileSuffix = "==="
	endMarker  = "===ENDFILE==="
)

// ValidateFileBlocks checks that a worker response contains well-formed
// ===FILE: path=== ... ===ENDFILE=== blocks. Returns ok=true when all
// checks pass, or ok=false with human-readable error descriptions.
func ValidateFileBlocks(output string) (bool, []string) {
	var errs []string

	fileCount := strings.Count(output, filePrefix)
	endCount := strings.Count(output, endMarker)

	if fileCount == 0 {
		return false, []string{"no ===FILE: markers found in output"}
	}

	if fileCount != endCount {
		errs = append(errs, fmt.Sprintf("unbalanced markers: %d ===FILE: vs %d ===ENDFILE===", fileCount, endCount))
	}

	// Walk through the output validating each FILE block.
	remaining := output
	blockNum := 0
	for {
		idx := strings.Index(remaining, filePrefix)
		if idx < 0 {
			break
		}
		blockNum++

		// Extract the path between "===FILE:" and the closing "===".
		afterPrefix := remaining[idx+len(filePrefix):]
		closingIdx := strings.Index(afterPrefix, fileSuffix)
		if closingIdx < 0 {
			errs = append(errs, fmt.Sprintf("block %d: ===FILE: marker has no closing ===", blockNum))
			break
		}

		path := strings.TrimSpace(afterPrefix[:closingIdx])
		if path == "" {
			errs = append(errs, fmt.Sprintf("block %d: empty file path", blockNum))
		}
		if strings.Contains(path, "..") {
			errs = append(errs, fmt.Sprintf("block %d: path %q contains '..' (path traversal)", blockNum, path))
		}

		// Check for matching ENDFILE after this FILE block.
		afterPath := afterPrefix[closingIdx+len(fileSuffix):]
		endIdx := strings.Index(afterPath, endMarker)
		if endIdx < 0 {
			errs = append(errs, fmt.Sprintf("block %d (%s): missing ===ENDFILE===", blockNum, path))
		}

		// Advance past this block.
		remaining = afterPrefix[closingIdx+len(fileSuffix):]
	}

	if len(errs) > 0 {
		return false, errs
	}
	return true, nil
}
