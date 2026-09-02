/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package reporter

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	RASSource      = "ras"
	LsPCISource    = "lspci"
	CardmgmtSource = "cardmgmt"

	TestPCIAddress  = "0000:1a:00.0"
	TestPCIAddress2 = "0000:1b:00.0"
	TestPCIAddress3 = "0000:1c:00.0"
	TestPCIAddress4 = "0000:1d:00.0"
)

func TestReporter(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Reporter Suite")
}
