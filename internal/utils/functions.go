/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package utils

import (
	"fmt"
	"time"
)

// ParseInterval parses a duration string and returns the duration.
// @param interval: duration string like "1h30m", "45m", or "2s"
// @return: time.Duration or error if the format is invalid
func ParseInterval(interval string) (time.Duration, error) {
	d, err := time.ParseDuration(interval)
	if err != nil {
		return 0, fmt.Errorf("invalid duration format %q: must be like '1h30m', '45m' or '2s'", interval)
	}

	return d, nil
}
