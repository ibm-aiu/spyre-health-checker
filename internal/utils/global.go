/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package utils

import (
	"os"
)

var NodeName string = os.Getenv("NODE_NAME")
var Namespace string = os.Getenv("NAMESPACE")
var PodName string = os.Getenv("POD_NAME")
